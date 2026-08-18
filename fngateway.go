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

var htmlAttrRegex = regexp.MustCompile(`(?i)\b(src|href|action)=(["'])(/[^"']*)`)

// InitFnGateway 注册飞牛网关直连代理路由
func InitFnGateway(base *gin.RouterGroup) {
	base.Any("/fngateway", handleFnGateway)
	base.Any("/fngateway/*action", handleFnGateway)
}

// handleFnGateway 飞牛网关核心反向代理处理器
func handleFnGateway(c *gin.Context) {
	// 获取后端监听端口
	serverPort := GetConfig().ServerPort
	if serverPort <= 0 {
		serverPort = 2298
	}
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
		},
		ModifyResponse: func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))

			// 改写重定向地址
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", rewriteGatewayLocation(loc))
			}

			// 优化流式响应标头
			if strings.HasPrefix(contentType, "text/event-stream") {
				resp.Header.Set("Cache-Control", "no-cache, no-transform")
				resp.Header.Set("X-Accel-Buffering", "no")
				resp.Header.Del("Content-Length")
				return nil
			}

			// 改写页面标签并注入补丁脚本
			if strings.Contains(contentType, "text/html") && resp.Body != nil {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return err
				}

				modified := rewriteFnGatewayHtml(bodyBytes)
				resp.Body = io.NopCloser(bytes.NewReader(modified))
				resp.ContentLength = int64(len(modified))
				resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
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
	if !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") {
		return loc
	}
	if loc == fnGatewayPrefix || strings.HasPrefix(loc, fnGatewayPrefix+"/") {
		return loc
	}
	return fnGatewayPrefix + loc
}

// rewriteFnGatewayHtml 改写静态资源标签并注入补丁脚本
func rewriteFnGatewayHtml(body []byte) []byte {
	// 正则替换属性路径
	modified := htmlAttrRegex.ReplaceAllFunc(body, func(match []byte) []byte {
		sub := htmlAttrRegex.FindSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		attr := string(sub[1])
		quote := string(sub[2])
		path := string(sub[3])
		// 排除协议相对路径与已带前缀路径
		if strings.HasPrefix(path, "//") || strings.HasPrefix(path, fnGatewayPrefix) {
			return match
		}
		newPath := fnGatewayPrefix + path
		return fmt.Appendf(nil, "%s=%s%s", attr, quote, newPath)
	})

	// 注入桥接脚本
	bridge := []byte(fnGatewayBridgeScript())
	lower := bytes.ToLower(modified)
	idx := bytes.Index(lower, []byte("<head"))
	if idx != -1 {
		closeIdx := bytes.IndexByte(lower[idx:], '>')
		if closeIdx != -1 {
			insertPos := idx + closeIdx + 1
			var res bytes.Buffer
			res.Grow(len(modified) + len(bridge))
			res.Write(modified[:insertPos])
			res.Write(bridge)
			res.Write(modified[insertPos:])
			return res.Bytes()
		}
	}

	var res bytes.Buffer
	res.Grow(len(modified) + len(bridge))
	res.Write(bridge)
	res.Write(modified)
	return res.Bytes()
}

// fnGatewayBridgeScript 生成前端运行时拦截补丁脚本
func fnGatewayBridgeScript() string {
	return `<script>
(function (prefix) {
  // 仅在当前页面位于网关前缀下时激活
  if (typeof window === "undefined" || !window.location || window.location.pathname.indexOf(prefix) !== 0) return;

  // crypto.randomUUID 兼容补丁
  var cryptoObject = window.crypto;
  if (cryptoObject && typeof cryptoObject.randomUUID !== "function" && typeof cryptoObject.getRandomValues === "function") {
    var getRandomValues = cryptoObject.getRandomValues.bind(cryptoObject);
    var randomUUID = function () {
      var bytes = new Uint8Array(16);
      getRandomValues(bytes);
      bytes[6] = (bytes[6] & 15) | 64;
      bytes[8] = (bytes[8] & 63) | 128;
      var hex = Array.from(bytes, function (byte) { return ("0" + byte.toString(16)).slice(-2); }).join("");
      return hex.slice(0, 8) + "-" + hex.slice(8, 12) + "-" + hex.slice(12, 16) + "-" + hex.slice(16, 20) + "-" + hex.slice(20);
    };
    var installRandomUUID = function (target) {
      try {
        Object.defineProperty(target, "randomUUID", { configurable: true, writable: true, value: randomUUID });
        return typeof target.randomUUID === "function";
      } catch (_) { return false; }
    };
    if (!installRandomUUID(cryptoObject) && Object.getPrototypeOf(cryptoObject)) installRandomUUID(Object.getPrototypeOf(cryptoObject));
  }

  // 全同源路由重写器
  var isAlreadyPrefixed = function (pathname) {
    return prefix !== "" && (pathname === prefix || pathname.indexOf(prefix + "/") === 0);
  };
  var toGatewayUrl = function (value) {
    if (!value) return null;
    var url;
    try { url = new URL(String(value), window.location.href); }
    catch (_) { return null; }
    // 仅拦截同源请求
    if (url.origin !== window.location.origin) return null;
    // 已包含前缀直接放行
    if (isAlreadyPrefixed(url.pathname)) return null;
    // 追加网关前缀
    var rawPath = url.pathname.indexOf('/') === 0 ? url.pathname : '/' + url.pathname;
    url.pathname = prefix + rawPath;
    return url;
  };

  // 拦截 Fetch API
  var nativeFetch = window.fetch.bind(window);
  window.fetch = function (input, init) {
    if (typeof Request !== "undefined" && input instanceof Request) {
      var mapped = toGatewayUrl(input.url);
      if (mapped !== null) input = new Request(mapped, input);
    } else {
      var mapped = toGatewayUrl(input);
      if (mapped !== null) input = mapped;
    }
    return nativeFetch(input, init);
  };

  // 拦截 XMLHttpRequest
  if (window.XMLHttpRequest) {
    var nativeXHROpen = window.XMLHttpRequest.prototype.open;
    window.XMLHttpRequest.prototype.open = function (method, url) {
      var mapped = toGatewayUrl(url);
      if (mapped !== null) {
        arguments[1] = mapped.toString();
      }
      return nativeXHROpen.apply(this, arguments);
    };
  }

  // 拦截 DOM 资源节点动态插入
  var rewriteElementNode = function (node) {
    if (!node || node.nodeType !== 1) return;
    var tag = node.tagName;
    if (tag === "SCRIPT" || tag === "IMG" || tag === "IFRAME" || tag === "AUDIO" || tag === "VIDEO") {
      var src = node.getAttribute("src") || node.src;
      var mapped = toGatewayUrl(src);
      if (mapped !== null) node.setAttribute("src", mapped.toString());
    } else if (tag === "LINK") {
      var href = node.getAttribute("href") || node.href;
      var mapped = toGatewayUrl(href);
      if (mapped !== null) node.setAttribute("href", mapped.toString());
    }
  };
  var nativeAppend = Element.prototype.append;
  if (nativeAppend) {
    Element.prototype.append = function () {
      for (var i = 0; i < arguments.length; i++) rewriteElementNode(arguments[i]);
      return nativeAppend.apply(this, arguments);
    };
  }
  var nativeAppendChild = Node.prototype.appendChild;
  Node.prototype.appendChild = function (node) {
    rewriteElementNode(node);
    return nativeAppendChild.call(this, node);
  };
  var nativeInsertBefore = Node.prototype.insertBefore;
  Node.prototype.insertBefore = function (node, reference) {
    rewriteElementNode(node);
    return nativeInsertBefore.call(this, node, reference);
  };

  // 拦截 EventSource 流式连接
  var nativeEventSource = window.EventSource;
  if (nativeEventSource) {
    window.EventSource = new Proxy(nativeEventSource, {
      construct: function (target, args, newTarget) {
        var mapped = toGatewayUrl(args[0]);
        if (mapped !== null) args = [mapped.toString()].concat(args.slice(1));
        return Reflect.construct(target, args, newTarget);
      }
    });
  }

  // 拦截 WebSocket 连接
  var nativeWebSocket = window.WebSocket;
  if (nativeWebSocket) {
    var page = new URL(window.location.href);
    var pagePort = page.port || (page.protocol === "https:" ? "443" : "80");
    window.WebSocket = new Proxy(nativeWebSocket, {
      construct: function (target, args, newTarget) {
        var url;
        try { url = new URL(String(args[0]), window.location.href); }
        catch (_) { return Reflect.construct(target, args, newTarget); }
        var socketPort = url.port || (url.protocol === "wss:" ? "443" : "80");
        if ((url.protocol === "ws:" || url.protocol === "wss:") &&
            url.hostname === page.hostname && socketPort === pagePort &&
            !isAlreadyPrefixed(url.pathname)) {
          var rawPath = url.pathname.indexOf('/') === 0 ? url.pathname : '/' + url.pathname;
          url.pathname = prefix + rawPath;
          args = [url.toString()].concat(args.slice(1));
        }
        return Reflect.construct(target, args, newTarget);
      }
    });
  }

  // 拦截 navigator.sendBeacon
  if (navigator && typeof navigator.sendBeacon === "function") {
    var nativeSendBeacon = navigator.sendBeacon.bind(navigator);
    navigator.sendBeacon = function (url, data) {
      var mapped = toGatewayUrl(url);
      return nativeSendBeacon(mapped !== null ? mapped.toString() : url, data);
    };
  }

  // 拦截 Web Worker 脚本路径
  if (window.Worker) {
    var nativeWorker = window.Worker;
    window.Worker = new Proxy(nativeWorker, {
      construct: function (target, args, newTarget) {
        var mapped = toGatewayUrl(args[0]);
        if (mapped !== null) args = [mapped.toString()].concat(args.slice(1));
        return Reflect.construct(target, args, newTarget);
      }
    });
  }
})("` + fnGatewayPrefix + `");
</script>`
}

// serveFnGatewayStatusPage 渲染符合系统设计规范的网关状态/错误页面
func serveFnGatewayStatusPage(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)

	currentStatus := state.Status()
	title := "无法连接到后端服务"
	desc := "服务响应异常，请检查后台运行状态"
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
		desc = "正在准备运行环境与初始化依赖，请稍候..."
		badgeClass = "badge-starting"
		badgeText = "启动中"
		isStarting = true
	case StatusBuilding:
		title = "服务构建中"
		desc = "正在拉取镜像或构建运行环境，准备就绪后将自动进入"
		badgeClass = "badge-starting"
		badgeText = "构建中"
		isStarting = true
	case StatusStopped:
		title = "服务尚未启动"
		desc = "底层模型服务当前处于停止状态，请在 DeepSeek Harness 控制面板中启动"
		badgeClass = "badge-stopped"
		badgeText = "已停止"
	}

	var detailsHTML template.HTML
	if errDetail != "" && !isStarting {
		reqInfo := ""
		if r != nil {
			reqInfo = fmt.Sprintf("请求路径: %s %s\n发生时间: %s\n", r.Method, r.URL.Path, time.Now().Format("2006-01-02 15:04:05"))
		}
		detailsHTML = template.HTML(fmt.Sprintf(`<details class="details-box">
      <summary>详情</summary>
      <pre>%s%s</pre>
    </details>`, template.HTMLEscapeString(reqInfo), template.HTMLEscapeString(errDetail)))
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
