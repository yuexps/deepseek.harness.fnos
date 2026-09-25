package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/auth_login.html
var authLoginPageTplContent string

//go:embed templates/pwa-icon.svg
var pwaIconSvgBytes []byte

var authLoginPageTpl = template.Must(template.New("auth_login").Parse(authLoginPageTplContent))

const (
	authCookieName      = "harness_session"
	authLegacyCookie    = "harness_auth"
	authLoginPath       = "/_harness_auth"
	authMaxAttempts     = 3
	authLockoutDuration = 1 * time.Hour
)

type clientAuthStatus struct {
	failedCount int
	lockUntil   time.Time
}

var (
	authLockMu       sync.Mutex
	clientAuthRecord = make(map[string]*clientAuthStatus)
)

func getClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func checkAuthLockout(clientIP string) (bool, time.Duration, int) {
	authLockMu.Lock()
	defer authLockMu.Unlock()

	status, exists := clientAuthRecord[clientIP]
	if !exists {
		return false, 0, authMaxAttempts
	}

	if status.failedCount >= authMaxAttempts {
		now := time.Now()
		if now.Before(status.lockUntil) {
			return true, status.lockUntil.Sub(now), 0
		}
		delete(clientAuthRecord, clientIP)
		return false, 0, authMaxAttempts
	}

	return false, 0, authMaxAttempts - status.failedCount
}

func recordAuthFailure(clientIP string) (bool, time.Duration, int) {
	authLockMu.Lock()
	defer authLockMu.Unlock()

	status, exists := clientAuthRecord[clientIP]
	if !exists {
		status = &clientAuthStatus{}
		clientAuthRecord[clientIP] = status
	}

	status.failedCount++
	if status.failedCount >= authMaxAttempts {
		status.lockUntil = time.Now().Add(authLockoutDuration)
		return true, authLockoutDuration, 0
	}

	return false, 0, authMaxAttempts - status.failedCount
}

func recordAuthSuccess(clientIP string) {
	authLockMu.Lock()
	delete(clientAuthRecord, clientIP)
	authLockMu.Unlock()
}

func getAuthToken(pwd string) string {
	sum := sha256.Sum256([]byte("harness_auth_salt:" + pwd))
	return hex.EncodeToString(sum[:])
}

func isValidAuthCookie(r *http.Request, pwd string) bool {
	expectedToken := getAuthToken(pwd)
	// 优先检查新 session cookie
	if c, err := r.Cookie(authCookieName); err == nil && c.Value != "" {
		if c.Value == expectedToken || c.Value == pwd {
			return true
		}
	}
	// 兼容旧 auth cookie
	if c, err := r.Cookie(authLegacyCookie); err == nil && c.Value != "" {
		if c.Value == expectedToken || c.Value == pwd {
			return true
		}
	}
	return false
}

// proxyWithAuth 密码中间件
func proxyWithAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		pwd := GetConfig().AccessPassword
		if pwd == "" {
			next.ServeHTTP(w, r)
			return
		}

		clientIP := getClientIP(r)
		isLocked, lockRemaining, remainingAttempts := checkAuthLockout(clientIP)

		// 处理登录验证请求
		if r.URL.Path == authLoginPath && r.Method == http.MethodPost {
			isJSONReq := strings.Contains(r.Header.Get("Content-Type"), "application/json") ||
				strings.Contains(r.Header.Get("Accept"), "application/json")

			if isLocked {
				mins := int(lockRemaining.Minutes()) + 1
				if isJSONReq {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusTooManyRequests)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"code":        http.StatusTooManyRequests,
						"locked":      true,
						"remain_mins": mins,
						"message":     fmt.Sprintf("密码错误达 3 次已锁定，请等待约 %d 分钟或重启服务", mins),
					})
					return
				}
				serveLoginPage(w, isLocked, lockRemaining, 0)
				return
			}

			// 获取输入的密码
			inputPwd := ""
			if isJSONReq {
				var reqBody struct {
					Password string `json:"password"`
				}
				bodyBytes, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(bodyBytes, &reqBody)
				inputPwd = reqBody.Password
			} else {
				_ = r.ParseForm()
				inputPwd = r.FormValue("password")
			}

			if inputPwd == pwd {
				recordAuthSuccess(clientIP)
				token := getAuthToken(pwd)
				cookie := &http.Cookie{
					Name:     authCookieName,
					Value:    token,
					Path:     "/",
					MaxAge:   86400 * 30, // 30天有效
					Expires:  time.Now().Add(30 * 24 * time.Hour),
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				}
				http.SetCookie(w, cookie)

				if isJSONReq {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"code":     0,
						"message":  "success",
						"token":    token,
						"redirect": "/",
					})
					return
				}
				http.Redirect(w, r, "/", http.StatusSeeOther)
			} else {
				lockedNow, remDuration, remAttempts := recordAuthFailure(clientIP)
				if isJSONReq {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					status := http.StatusUnauthorized
					msg := fmt.Sprintf("密码错误，还可尝试 %d 次", remAttempts)
					if lockedNow {
						status = http.StatusTooManyRequests
						mins := int(remDuration.Minutes()) + 1
						msg = fmt.Sprintf("密码错误达 3 次已锁定，请等待约 %d 分钟或重启服务", mins)
					}
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"code":               status,
						"locked":             lockedNow,
						"remaining_attempts": remAttempts,
						"message":            msg,
					})
					return
				}
				serveLoginPage(w, lockedNow, remDuration, remAttempts)
			}
			return
		}

		// 放行公开静态元数据（避免 PWA 清单与图标因浏览器默认无凭证请求而触发登录页拦截）
		if r.URL.Path == "/manifest.webmanifest" || r.URL.Path == "/favicon.svg" || r.URL.Path == "/pwa-icon.svg" {
			if r.URL.Path == "/pwa-icon.svg" && len(pwaIconSvgBytes) > 0 {
				w.Header().Set("Content-Type", "image/svg+xml")
				w.Header().Set("Cache-Control", "public, max-age=86400")
				_, _ = w.Write(pwaIconSvgBytes)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// 校验 cookie
		if !isValidAuthCookie(r, pwd) {
			serveLoginPage(w, isLocked, lockRemaining, remainingAttempts)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func serveLoginPage(w http.ResponseWriter, isLocked bool, lockRemaining time.Duration, remainingAttempts int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var errorHTML template.HTML
	buttonText := "进入"

	if isLocked {
		w.WriteHeader(http.StatusTooManyRequests)
		mins := int(lockRemaining.Minutes()) + 1
		errorHTML = template.HTML(fmt.Sprintf(`<div class="err" id="errMsg">
      <svg width="15" height="15" viewBox="0 0 20 20" fill="currentColor">
        <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clip-rule="evenodd"/>
      </svg>
      <span id="errText">密码错误达 3 次已锁定，请等待约 %d 分钟或重启服务</span>
    </div>`, mins))
		buttonText = "已锁定冷却中"
	} else if remainingAttempts < authMaxAttempts {
		w.WriteHeader(http.StatusUnauthorized)
		errorHTML = template.HTML(fmt.Sprintf(`<div class="err" id="errMsg">
      <svg width="15" height="15" viewBox="0 0 20 20" fill="currentColor">
        <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clip-rule="evenodd"/>
      </svg>
      <span id="errText">密码错误，还可尝试 %d 次</span>
    </div>`, remainingAttempts))
	}

	data := struct {
		IsLocked      bool
		ButtonText    string
		ErrorHTML     template.HTML
		AuthLoginPath string
	}{
		IsLocked:      isLocked,
		ButtonText:    buttonText,
		ErrorHTML:     errorHTML,
		AuthLoginPath: authLoginPath,
	}

	_ = authLoginPageTpl.Execute(w, data)
}

var (
	proxyMu     sync.Mutex
	proxyHTTP   *http.Server
	proxyTarget *url.URL
	proxyAddr   string
)

func updateReverseProxyTarget() {
	cfg := GetConfig()
	proxyTarget, _ = url.Parse(fmt.Sprintf("http://127.0.0.1:%d", cfg.GetServerPort()))
	proxyAddr = fmt.Sprintf("0.0.0.0:%d", cfg.GetProxyPort())
}

func startReverseProxy() error {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	return startReverseProxyLocked()
}

func startReverseProxyLocked() error {
	if proxyHTTP != nil {
		return nil
	}

	updateReverseProxyTarget()

	errHandler := func(w http.ResponseWriter, r *http.Request, err error) {
		// 过滤客户端主动断开连接/取消请求的正常行为
		if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			return
		}
		LogWarning("[代理] 请求转发失败 [%s]: %s", proxyAddr, err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "bad_gateway",
			"message": proxyErrMessage(),
			"detail":  err.Error(),
		})
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(proxyTarget)
			pr.SetXForwarded()
			// 统一回环 Host 标头，保证上游计算的 authority 恒定
			pr.Out.Host = proxyTarget.Host
			pr.Out.Header.Set("Host", proxyTarget.Host)
			// 改写为目标同源 Origin，保留标头供插件使用并防止上游 CSRF 校验失败
			if pr.Out.Header.Get("Origin") != "" {
				pr.Out.Header.Set("Origin", fmt.Sprintf("%s://%s", proxyTarget.Scheme, proxyTarget.Host))
			}
			// 改写为 same-origin，防止跨站/iframe 标记被上游拦截并保留标头
			if pr.Out.Header.Get("Sec-Fetch-Site") != "" {
				pr.Out.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			// 禁用压缩以便代理层注入 Polyfill
			pr.Out.Header.Set("Accept-Encoding", "identity")

			// 注入服务端代持凭据，解耦对客户端 Cookie 的依赖
			if session := GetDshSessionCookie(); session != "" {
				pr.Out.Header.Set("Cookie", appendDshSessionCookie(pr.In.Header.Get("Cookie"), session))
			}

			// 还原被折叠的 /plugins/?? 多路复用请求
			if pr.Out.URL.Path == "/plugins/" && !strings.HasPrefix(pr.Out.URL.RawQuery, "?") && strings.Contains(pr.Out.URL.RawQuery, "client.js") {
				pr.Out.URL.RawQuery = "?" + pr.Out.URL.RawQuery
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// 上游鉴权失败时失效服务端代持凭据
			if resp.StatusCode == http.StatusUnauthorized {
				InvalidateDshSession()
			}

			// 改写上游重定向地址，去除回环主机头防止协议漂移
			if loc := resp.Header.Get("Location"); loc != "" {
				if u, err := url.Parse(loc); err == nil && u.Host != "" {
					if strings.HasPrefix(u.Host, "127.0.0.1") || strings.HasPrefix(u.Host, "localhost") {
						u.Scheme = ""
						u.Host = ""
						resp.Header.Set("Location", u.String())
					}
				}
			}

			contentType := strings.ToLower(resp.Header.Get("Content-Type"))

			// 处理 SSE 流式响应标头
			if strings.HasPrefix(contentType, "text/event-stream") {
				resp.Header.Set("Cache-Control", "no-cache, no-transform")
				resp.Header.Set("X-Accel-Buffering", "no")
				resp.Header.Del("Content-Length")
				return nil
			}

			// 拦截 HTML 注入 Polyfill 并禁用强缓存
			if strings.Contains(contentType, "text/html") && resp.Body != nil {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return err
				}

				modified := injectHtmlPolyfill(bodyBytes)
				resp.Body = io.NopCloser(bytes.NewReader(modified))
				resp.ContentLength = int64(len(modified))
				resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
				applyDshHtmlNoStore(resp.Header)
			}

			// 拦截并改写 PWA Web App Manifest 的应用图标路径
			if (strings.Contains(contentType, "manifest+json") || (resp.Request != nil && strings.HasSuffix(resp.Request.URL.Path, ".webmanifest"))) && resp.Body != nil {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err == nil {
					modified := rewriteProxyManifest(bodyBytes)
					resp.Body = io.NopCloser(bytes.NewReader(modified))
					resp.ContentLength = int64(len(modified))
					resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
					resp.Header.Set("Content-Type", "application/manifest+json; charset=utf-8")
				}
			}

			return nil
		},
		ErrorHandler: errHandler,
	}

	// 建立 TCP 监听器
	ln, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		LogWarning("[代理] 端口监听失败 [%s]: %s", proxyAddr, err)
		return err
	}

	proxyHTTP = &http.Server{Handler: proxyWithAuth(proxy)}

	LogInfo("[代理] 已启动监听 [%s → %s]", proxyAddr, proxyTarget.String())

	go func() {
		if err := proxyHTTP.Serve(ln); err != nil && !isExpectedCloseErr(err) {
			LogWarning("[代理] HTTP 代理服务异常退出: %s", err)
		}
	}()

	return nil
}

func isExpectedCloseErr(err error) bool {
	if err == nil || err == http.ErrServerClosed || err == net.ErrClosed || errors.Is(err, context.Canceled) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "server closed") ||
		strings.Contains(msg, "closed network connection") ||
		strings.Contains(msg, "context canceled")
}

func stopReverseProxy() {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	stopReverseProxyLocked()
}

func stopReverseProxyLocked() {
	if proxyHTTP == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = proxyHTTP.Shutdown(ctx)
	proxyHTTP = nil
	LogInfo("[代理] 监听已停止")
}

// proxyErrMessage 根据当前服务状态给出准确的代理错误提示
func proxyErrMessage() string {
	switch state.Status() {
	case StatusStarting:
		return "服务正在启动"
	case StatusRunning:
		return "服务响应异常"
	case StatusBuilding:
		return "服务正在部署更新"
	case StatusSnapshotting:
		return "服务快照维护中"
	case StatusStopped:
		return "服务未运行"
	default:
		return "无法连接到后端服务"
	}
}

// restartReverseProxy 按最新配置重启反向代理
func restartReverseProxy() {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	stopReverseProxyLocked()
	if state.Status() != StatusRunning {
		return
	}
	LogInfo("[代理] 配置变更，执行热重载")
	if err := startReverseProxyLocked(); err != nil {
		LogWarning("[代理] 热重载失败: %s", err)
	}
}

const httpPolyfillScript = `<style>[data-slot="settings.action"] { display: none !important; }</style><script>try{window.__DSH_TRANSPORT__=Object.assign(window.__DSH_TRANSPORT__||{},{ownsHost:true});}catch(_){}</script>`

// injectHtmlPolyfill 将兼容补丁注入 HTML 的 head 头部
func injectHtmlPolyfill(body []byte) []byte {
	return injectHtmlHead(body, []byte(httpPolyfillScript))
}


// rewriteProxyManifest 注入修改 PWA manifest 中的应用图标为 /pwa-icon.svg
func rewriteProxyManifest(body []byte) []byte {
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return body
	}
	if icons, ok := manifest["icons"].([]any); ok {
		for _, ic := range icons {
			if icMap, ok := ic.(map[string]any); ok {
				icMap["src"] = "/pwa-icon.svg"
			}
		}
	}
	newBytes, err := json.Marshal(manifest)
	if err != nil {
		return body
	}
	return newBytes
}

// appendDshSessionCookie 组装发往后端的 Cookie，剥离客户端旧凭据并注入服务端凭据
func appendDshSessionCookie(clientCookie, sessionCookie string) string {
	var kept []string
	for _, part := range strings.Split(clientCookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "dsh-auth-") {
			continue
		}
		kept = append(kept, part)
	}
	if sessionCookie != "" {
		kept = append(kept, sessionCookie)
	}
	return strings.Join(kept, "; ")
}


// applyDshHtmlNoStore 禁用 HTML 强缓存，避免复用旧版本资源
func applyDshHtmlNoStore(header http.Header) {
	header.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
	header.Del("ETag")
	header.Del("Last-Modified")
}

