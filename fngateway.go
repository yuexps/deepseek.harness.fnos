package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed templates/gateway_status.html
var gatewayStatusPageTplContent string

var gatewayStatusPageTpl = template.Must(template.New("gateway_status").Parse(gatewayStatusPageTplContent))

const fnGatewayPrefix = "/app/deepseek-harness/fngateway"

var manifestLinkRegex = regexp.MustCompile(`(?i)<link\b[^>]*\brel=["']manifest["'][^>]*>`)
var cookiePathRegex = regexp.MustCompile(`(?i)\bpath\s*=\s*/(;|$)`)

// InitFnGateway 注册飞牛网关直连代理路由
func InitFnGateway(base *gin.RouterGroup) {
	base.Any("/fngateway", handleFnGateway)
	base.Any("/fngateway/*action", handleFnGateway)
}

// handleFnGateway 飞牛网关核心反向代理处理器
func handleFnGateway(c *gin.Context) {
	// 规范化裸路径为带末尾斜杠的重定向路径
	if c.Request.URL.Path == fnGatewayPrefix {
		target := fnGatewayPrefix + "/"
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusMovedPermanently, target)
		return
	}

	// 获取后端监听端口
	serverPort := GetConfig().GetServerPort()
	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", serverPort))

	// 构建反向代理
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(targetURL)
			pr.SetXForwarded()

			// 剥除网关前缀
			p := strings.TrimPrefix(pr.Out.URL.Path, fnGatewayPrefix)
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			pr.Out.URL.Path = p
			if pr.Out.URL.RawPath != "" {
				rawP := strings.TrimPrefix(pr.Out.URL.RawPath, fnGatewayPrefix)
				if !strings.HasPrefix(rawP, "/") {
					rawP = "/" + rawP
				}
				pr.Out.URL.RawPath = rawP
			}

			// 改写回环请求头与安全上下文
			pr.Out.Host = fmt.Sprintf("127.0.0.1:%d", serverPort)
			pr.Out.Header.Set("Host", fmt.Sprintf("127.0.0.1:%d", serverPort))
			pr.Out.Header.Set("Origin", fmt.Sprintf("http://127.0.0.1:%d", serverPort))
			pr.Out.Header.Set("Sec-Fetch-Site", "same-origin")
			pr.Out.Header.Set("Accept-Encoding", "identity")

			// 注入服务端代持凭据
			if session := GetDshSessionCookie(); session != "" {
				pr.Out.Header.Set("Cookie", appendDshSessionCookie(c.Request.Header.Get("Cookie"), session))
			}

			// 还原被折叠的 /plugins/?? 多路复用请求
			if (p == "/plugins/" || strings.HasSuffix(p, "/plugins/")) && !strings.HasPrefix(pr.Out.URL.RawQuery, "?") && strings.Contains(pr.Out.URL.RawQuery, "client.js") {
				pr.Out.URL.RawQuery = "?" + pr.Out.URL.RawQuery
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// 上游鉴权失败时失效服务端代持凭据
			if resp.StatusCode == http.StatusUnauthorized {
				InvalidateDshSession()
			}

			contentType := strings.ToLower(resp.Header.Get("Content-Type"))

			// 改写重定向地址
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", rewriteGatewayLocation(loc))
			}

			// 改写 Cookie 作用域路径
			if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
				resp.Header.Del("Set-Cookie")
				for _, ck := range cookies {
					resp.Header.Add("Set-Cookie", rewriteGatewayCookie(ck))
				}
			}

			// 优化流式响应标头
			if strings.HasPrefix(contentType, "text/event-stream") {
				resp.Header.Set("Cache-Control", "no-cache, no-transform")
				resp.Header.Set("X-Accel-Buffering", "no")
				resp.Header.Del("Content-Length")
				return nil
			}

			// 改写页面标签并注入核心补丁脚本
			if strings.Contains(contentType, "text/html") && resp.Body != nil {
				// 移除 CSP 标头
				resp.Header.Del("Content-Security-Policy")
				resp.Header.Del("Content-Security-Policy-Report-Only")

				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return err
				}

				modified := rewriteFnGatewayHtml(bodyBytes)
				resp.Body = io.NopCloser(bytes.NewReader(modified))
				resp.ContentLength = int64(len(modified))
				resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
				applyDshHtmlNoStore(resp.Header)
			}

			// 拦截并改写 PWA Web App Manifest 的子路径作用域
			if (strings.Contains(contentType, "manifest+json") || (resp.Request != nil && strings.HasSuffix(resp.Request.URL.Path, ".webmanifest"))) && resp.Body != nil {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err == nil {
					modified := rewriteGatewayManifest(bodyBytes)
					resp.Body = io.NopCloser(bytes.NewReader(modified))
					resp.ContentLength = int64(len(modified))
					resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
					resp.Header.Set("Content-Type", "application/manifest+json; charset=utf-8")
				}
			}

			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// API / 数据请求返回结构化 JSON
			if !strings.Contains(r.Header.Get("Accept"), "text/html") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   "bad_gateway",
					"status":  state.Status(),
					"message": proxyErrMessage(),
					"detail":  err.Error(),
				})
				return
			}

			// HTML 页面请求渲染符合系统风格的状态指示页
			serveFnGatewayStatusPage(w, r, err)
		},
	}

	proxy.ServeHTTP(c.Writer, c.Request)
	c.Abort()
}

// rewriteGatewayLocation 重写重定向地址
func rewriteGatewayLocation(loc string) string {
	if loc == "" {
		return loc
	}
	// 剥除回环 Host，避免协议或端口漂移
	if u, err := url.Parse(loc); err == nil && u.Host != "" {
		if strings.HasPrefix(u.Host, "127.0.0.1") || strings.HasPrefix(u.Host, "localhost") {
			u.Scheme = ""
			u.Host = ""
			loc = u.String()
		}
	}
	if !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") {
		return loc
	}
	if loc == fnGatewayPrefix || strings.HasPrefix(loc, fnGatewayPrefix+"/") {
		return loc
	}
	return fnGatewayPrefix + loc
}

// rewriteGatewayCookie 重写 Cookie 作用域路径
func rewriteGatewayCookie(ck string) string {
	return cookiePathRegex.ReplaceAllString(ck, "Path="+fnGatewayPrefix+"/$1")
}

// rewriteGatewayManifest 改写 PWA Web App Manifest 中的 scope, start_url 与图标子路径
func rewriteGatewayManifest(body []byte) []byte {
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return body
	}
	manifest["scope"] = fnGatewayPrefix + "/"
	manifest["start_url"] = fnGatewayPrefix + "/"
	manifest["id"] = fnGatewayPrefix + "/"
	if icons, ok := manifest["icons"].([]any); ok {
		for _, ic := range icons {
			if icMap, ok := ic.(map[string]any); ok {
				if src, ok := icMap["src"].(string); ok && strings.HasPrefix(src, "/") && !strings.HasPrefix(src, fnGatewayPrefix) {
					icMap["src"] = fnGatewayPrefix + src
				}
			}
		}
	}
	newBytes, err := json.Marshal(manifest)
	if err != nil {
		return body
	}
	return newBytes
}

// rewriteFnGatewayHtml 改写页面标签并注入核心补丁
func rewriteFnGatewayHtml(body []byte) []byte {
	// 注入凭据属性以放行 manifest 请求
	modified := manifestLinkRegex.ReplaceAllFunc(body, func(match []byte) []byte {
		if !bytes.Contains(bytes.ToLower(match), []byte("crossorigin")) {
			return bytes.Replace(match, []byte("<link"), []byte("<link crossorigin=\"use-credentials\""), 1)
		}
		return match
	})

	// 兜底补齐缺失的 base 标签
	if !bytes.Contains(bytes.ToLower(modified), []byte("<base")) {
		modified = injectHtmlHead(modified, []byte(`<base href="./">`))
	}

	// 注入特权契约声明与样式隐藏补丁
	return injectHtmlHead(modified, []byte(httpPolyfillScript))
}

// injectHtmlHead 将补丁脚本注入 HTML 的 head 头部
func injectHtmlHead(body, script []byte) []byte {
	lower := bytes.ToLower(body)
	idx := bytes.Index(lower, []byte("<head"))
	if idx != -1 {
		closeIdx := bytes.IndexByte(lower[idx:], '>')
		if closeIdx != -1 {
			insertPos := idx + closeIdx + 1
			var res bytes.Buffer
			res.Grow(len(body) + len(script))
			res.Write(body[:insertPos])
			res.Write(script)
			res.Write(body[insertPos:])
			return res.Bytes()
		}
	}
	var res bytes.Buffer
	res.Grow(len(body) + len(script))
	res.Write(script)
	res.Write(body)
	return res.Bytes()
}

// serveFnGatewayStatusPage 渲染符合系统设计规范的网关状态/错误页面
func serveFnGatewayStatusPage(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)

	currentStatus := state.Status()
	title := "无法连接到后端服务"
	desc := "后端服务暂未响应"
	badgeClass := "badge-err"
	badgeText := "异常"
	isStarting := false
	errDetail := ""
	if err != nil {
		errDetail = err.Error()
	}

	switch currentStatus {
	case StatusStarting:
		title = "服务启动中"
		desc = "正在准备运行环境与初始化依赖"
		badgeClass = "badge-starting"
		badgeText = "启动中"
		isStarting = true
	case StatusBuilding:
		title = "服务部署中"
		desc = "正在部署与启动核心运行环境"
		badgeClass = "badge-starting"
		badgeText = "部署中"
		isStarting = true
	case StatusSnapshotting:
		title = "快照维护中"
		desc = "正在执行快照备份或还原操作"
		badgeClass = "badge-starting"
		badgeText = "快照中"
		isStarting = true
	case StatusStopped:
		title = "服务未运行"
		desc = "底层模型服务当前处于停止状态"
		badgeClass = "badge-stopped"
		badgeText = "已停止"
	}

	var detailsHTML template.HTML
	if errDetail != "" && !isStarting {
		reqInfo := ""
		if r != nil {
			reqInfo = fmt.Sprintf("请求路径: %s %s\n发生时间: %s\n", r.Method, r.URL.Path, time.Now().Format("2006-01-02 15:04:05"))
		}
		detailsHTML = template.HTML(fmt.Sprintf(`<div class="details-box">
      <pre>%s%s</pre>
    </div>`, template.HTMLEscapeString(reqInfo), template.HTMLEscapeString(errDetail)))
	}

	data := struct {
		Title       string
		Desc        string
		BadgeClass  string
		BadgeText   string
		IsStarting  bool
		DetailsHTML template.HTML
	}{
		Title:       title,
		Desc:        desc,
		BadgeClass:  badgeClass,
		BadgeText:   badgeText,
		IsStarting:  isStarting,
		DetailsHTML: detailsHTML,
	}

	_ = gatewayStatusPageTpl.Execute(w, data)
}
