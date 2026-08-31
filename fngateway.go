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

var htmlAttrRegex = regexp.MustCompile(`(?i)\b(src|href|action|poster)\s*=\s*(["'])(/[^"']*)`)
var manifestLinkRegex = regexp.MustCompile(`(?i)<link\b[^>]*\brel=["']manifest["'][^>]*>`)
var cookiePathRegex = regexp.MustCompile(`(?i)\bpath\s*=\s*/(;|$)`)

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

			// 若访问网关根路径且未携带官方会话 Cookie，自动注入 Launch Token 换取会话
			if (p == "" || p == "/" || p == "/index.html") && !hasDshAuthCookie(c.Request.Header.Get("Cookie")) {
				if token := GetCurrentLaunchToken(); token != "" && !pr.Out.URL.Query().Has("token") {
					q := pr.Out.URL.Query()
					q.Set("token", token)
					pr.Out.URL.RawQuery = q.Encode()
				}
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// 拦截 401 鉴权失败，若具备新 Token 则自动重定向刷新换票
			if resp.StatusCode == http.StatusUnauthorized {
				if token := GetCurrentLaunchToken(); token != "" {
					bodyBytes, err := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if err == nil && strings.Contains(string(bodyBytes), "dsh web authentication required") {
						resp.StatusCode = http.StatusSeeOther
						resp.Header.Set("Location", fmt.Sprintf("%s/?token=%s", fnGatewayPrefix, url.QueryEscape(token)))
						resp.Header.Set("Cache-Control", "no-store")
						resp.Header.Del("Content-Length")
						resp.Body = io.NopCloser(bytes.NewReader(nil))
						resp.ContentLength = 0
						return nil
					}
					resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				}
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

			// 改写页面标签并注入补丁脚本
			if strings.Contains(contentType, "text/html") && resp.Body != nil {
				// 移除 CSP 标头，避免阻断注入的网关适配脚本执行
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

	// 确保 manifest 标签携带凭证发起请求，避免被飞牛 OS 网关鉴权拦截返回 invalid token
	modified = manifestLinkRegex.ReplaceAllFunc(modified, func(match []byte) []byte {
		if !bytes.Contains(bytes.ToLower(match), []byte("crossorigin")) {
			return bytes.Replace(match, []byte("<link"), []byte("<link crossorigin=\"use-credentials\""), 1)
		}
		return match
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
	return `<base href="` + fnGatewayPrefix + `/"><style>[data-slot="settings.action"] { display: none !important; }</style><script>
(function (prefix) {
  if (typeof window === "undefined" || !window.location) return;
  if (window.location.pathname.indexOf(prefix) !== 0 && window.location.pathname !== prefix) return;

  var isAlreadyPrefixed = function (pathname) {
    return prefix !== "" && (pathname === prefix || pathname.indexOf(prefix + "/") === 0);
  };

  var toGatewayUrl = function (value) {
    if (!value) return null;
    var str = String(value).trim();
    if (str.indexOf("blob:") === 0 || str.indexOf("data:") === 0 || str.indexOf("javascript:") === 0 || str.indexOf("about:") === 0 || str.indexOf("#") === 0) return null;
    var url;
    try { url = new URL(str, window.location.href); }
    catch (_) { return null; }
    if (url.protocol !== "http:" && url.protocol !== "https:" && url.protocol !== "ws:" && url.protocol !== "wss:") return null;
    if (url.origin !== window.location.origin) return null;
    if (isAlreadyPrefixed(url.pathname)) return null;
    var rawPath = url.pathname.indexOf('/') === 0 ? url.pathname : '/' + url.pathname;
    url.pathname = prefix + rawPath;
    return url;
  };

  var toGatewaySrcset = function (srcsetStr) {
    if (!srcsetStr || typeof srcsetStr !== "string") return srcsetStr;
    return srcsetStr.split(",").map(function (part) {
      var item = part.trim();
      if (!item) return item;
      var segs = item.split(/\s+/);
      var mapped = toGatewayUrl(segs[0]);
      if (mapped !== null) segs[0] = mapped.toString();
      return segs.join(" ");
    }).join(", ");
  };

  var rewriteHtmlString = function (html) {
    if (typeof html !== "string" || html.indexOf("/") === -1) return html;
    var htmlAttrRe = new RegExp("\\b(src|href|action|poster)=([\"'])(/[^\"']*)\\2", "gi");
    return html.replace(htmlAttrRe, function (match, attr, quote, path) {
      if (isAlreadyPrefixed(path) || path.indexOf("//") === 0) return match;
      return attr + "=" + quote + prefix + path + quote;
    });
  };

  var installBridge = function (targetWindow) {
    if (!targetWindow || targetWindow.__fnGatewayBridgeReady) return;
    targetWindow.__fnGatewayBridgeReady = true;

    // 拦截 Location 原型（pathname、assign、replace）
    if (targetWindow.Location && targetWindow.Location.prototype) {
      var locProto = targetWindow.Location.prototype;
      var locPathDesc = Object.getOwnPropertyDescriptor(locProto, "pathname");
      if (locPathDesc && locPathDesc.get && locPathDesc.configurable) {
        var nativeLocPathGet = locPathDesc.get;
        var nativeLocPathSet = locPathDesc.set;
        try {
          Object.defineProperty(locProto, "pathname", {
            get: function () {
              var p = nativeLocPathGet.call(this);
              if (isAlreadyPrefixed(p)) {
                var stripped = p.slice(prefix.length);
                return stripped.indexOf("/") === 0 ? stripped : "/" + stripped;
              }
              return p;
            },
            set: function (val) {
              if (nativeLocPathSet) {
                if (typeof val === "string" && val.indexOf("/") === 0 && !isAlreadyPrefixed(val)) {
                  val = prefix + val;
                }
                return nativeLocPathSet.call(this, val);
              }
            },
            configurable: true,
            enumerable: true
          });
        } catch (_) {}
      }

      if (locProto.assign) {
        var nativeAssign = locProto.assign;
        locProto.assign = function (url) {
          var mapped = toGatewayUrl(url);
          return nativeAssign.call(this, mapped !== null ? mapped.toString() : url);
        };
      }
      if (locProto.replace) {
        var nativeReplace = locProto.replace;
        locProto.replace = function (url) {
          var mapped = toGatewayUrl(url);
          return nativeReplace.call(this, mapped !== null ? mapped.toString() : url);
        };
      }
    }

    // crypto.randomUUID 兼容补丁
    var cryptoObject = targetWindow.crypto;
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

    // 拦截 Fetch API
    if (targetWindow.fetch) {
      var nativeFetch = targetWindow.fetch.bind(targetWindow);
      targetWindow.fetch = function (input, init) {
        if (typeof Request !== "undefined" && input instanceof Request) {
          var mapped = toGatewayUrl(input.url);
          if (mapped !== null) {
            try {
              input = new Request(mapped.toString(), input);
            } catch (_) {}
          }
        } else {
          var mapped = toGatewayUrl(input);
          if (mapped !== null) input = mapped.toString();
        }
        return nativeFetch(input, init);
      };
    }

    // 拦截 XMLHttpRequest
    if (targetWindow.XMLHttpRequest) {
      var nativeXHROpen = targetWindow.XMLHttpRequest.prototype.open;
      targetWindow.XMLHttpRequest.prototype.open = function (method, url) {
        var mapped = toGatewayUrl(url);
        if (mapped !== null) arguments[1] = mapped.toString();
        return nativeXHROpen.apply(this, arguments);
      };
    }

    // 属性描述符 Setter 拦截器
    var hookProperty = function (proto, prop, isSrcset) {
      if (!proto) return;
      var desc = Object.getOwnPropertyDescriptor(proto, prop);
      if (!desc || !desc.set) return;
      var nativeSet = desc.set;
      Object.defineProperty(proto, prop, {
        set: function (val) {
          if (isSrcset) return nativeSet.call(this, toGatewaySrcset(val));
          var mapped = toGatewayUrl(val);
          return nativeSet.call(this, mapped !== null ? mapped.toString() : val);
        },
        get: desc.get,
        configurable: true,
        enumerable: true
      });
    };

    // 批量劫持关键 DOM 原型属性
    if (targetWindow.HTMLImageElement) {
      hookProperty(targetWindow.HTMLImageElement.prototype, "src", false);
      hookProperty(targetWindow.HTMLImageElement.prototype, "srcset", true);
    }
    if (targetWindow.HTMLLinkElement) hookProperty(targetWindow.HTMLLinkElement.prototype, "href", false);
    if (targetWindow.HTMLAnchorElement) hookProperty(targetWindow.HTMLAnchorElement.prototype, "href", false);
    if (targetWindow.HTMLIFrameElement) hookProperty(targetWindow.HTMLIFrameElement.prototype, "src", false);
    if (targetWindow.HTMLMediaElement) hookProperty(targetWindow.HTMLMediaElement.prototype, "src", false);
    if (targetWindow.HTMLVideoElement) {
      hookProperty(targetWindow.HTMLVideoElement.prototype, "poster", false);
    }
    if (targetWindow.HTMLSourceElement) {
      hookProperty(targetWindow.HTMLSourceElement.prototype, "src", false);
      hookProperty(targetWindow.HTMLSourceElement.prototype, "srcset", true);
    }
    if (targetWindow.HTMLTrackElement) hookProperty(targetWindow.HTMLTrackElement.prototype, "src", false);
    if (targetWindow.HTMLInputElement) hookProperty(targetWindow.HTMLInputElement.prototype, "src", false);
    if (targetWindow.HTMLFormElement) hookProperty(targetWindow.HTMLFormElement.prototype, "action", false);
    if (targetWindow.HTMLObjectElement) hookProperty(targetWindow.HTMLObjectElement.prototype, "data", false);
    if (targetWindow.HTMLEmbedElement) hookProperty(targetWindow.HTMLEmbedElement.prototype, "src", false);

    // 拦截 setAttribute 与 setAttributeNS
    if (targetWindow.Element) {
      var nativeSetAttr = targetWindow.Element.prototype.setAttribute;
      targetWindow.Element.prototype.setAttribute = function (name, value) {
        var n = String(name).toLowerCase();
        var tag = (this.tagName || "").toUpperCase();
        if (n === "src" || n === "href" || n === "action" || n === "poster" || (n === "data" && tag === "OBJECT")) {
          var mapped = toGatewayUrl(value);
          if (mapped !== null) value = mapped.toString();
        } else if (n === "srcset") {
          value = toGatewaySrcset(value);
        }
        return nativeSetAttr.call(this, name, value);
      };

      if (targetWindow.Element.prototype.setAttributeNS) {
        var nativeSetAttrNS = targetWindow.Element.prototype.setAttributeNS;
        targetWindow.Element.prototype.setAttributeNS = function (ns, name, value) {
          var n = String(name).toLowerCase();
          var tag = (this.tagName || "").toUpperCase();
          if (n === "src" || n === "href" || n === "action" || n === "poster" || (n === "data" && tag === "OBJECT") || n.indexOf("href") !== -1) {
            var mapped = toGatewayUrl(value);
            if (mapped !== null) value = mapped.toString();
          }
          return nativeSetAttrNS.call(this, ns, name, value);
        };
      }

      // 拦截 innerHTML 与 insertAdjacentHTML
      var innerDesc = Object.getOwnPropertyDescriptor(targetWindow.Element.prototype, "innerHTML");
      if (innerDesc && innerDesc.set) {
        var nativeInnerSet = innerDesc.set;
        Object.defineProperty(targetWindow.Element.prototype, "innerHTML", {
          set: function (val) {
            return nativeInnerSet.call(this, rewriteHtmlString(val));
          },
          get: innerDesc.get,
          configurable: true,
          enumerable: true
        });
      }

      if (targetWindow.Element.prototype.insertAdjacentHTML) {
        var nativeInsertAdjHTML = targetWindow.Element.prototype.insertAdjacentHTML;
        targetWindow.Element.prototype.insertAdjacentHTML = function (pos, html) {
          return nativeInsertAdjHTML.call(this, pos, rewriteHtmlString(html));
        };
      }
    }

    // 拦截 <a> 标签点击
    targetWindow.addEventListener("click", function (e) {
      var target = e.target;
      while (target && target.tagName !== "A") {
        target = target.parentElement;
      }
      if (target && target.tagName === "A") {
        var href = target.getAttribute("href") || target.href;
        var mapped = toGatewayUrl(href);
        if (mapped !== null) {
          target.setAttribute("href", mapped.toString());
          if (target.href) target.href = mapped.toString();
        }
      }
    }, true);

    // 拦截 DOM 节点动态插入并自动穿透同源 iframe
    var injectIframe = function (iframeEl) {
      try {
        if (!iframeEl || iframeEl.__bridgeHooked) return;
        iframeEl.__bridgeHooked = true;
        var hookWin = function () {
          try {
            var win = iframeEl.contentWindow;
            if (win && win !== targetWindow) {
              installBridge(win);
            }
          } catch (_) {}
        };
        iframeEl.addEventListener("load", hookWin);
        hookWin();
      } catch (_) {}
    };

    var rewriteElementNode = function (node) {
      if (!node || node.nodeType !== 1) return;
      var tag = node.tagName;
      if (tag === "SCRIPT" || tag === "IMG" || tag === "IFRAME" || tag === "AUDIO" || tag === "VIDEO" || tag === "EMBED") {
        var rawAttr = node.getAttribute("src");
        if (rawAttr && !isAlreadyPrefixed(rawAttr)) {
          var mapped = toGatewayUrl(rawAttr);
          if (mapped !== null) node.setAttribute("src", mapped.toString());
        } else if (!rawAttr && node.src && !isAlreadyPrefixed(node.src)) {
          var mapped = toGatewayUrl(node.src);
          if (mapped !== null) node.src = mapped.toString();
        }
        if (tag === "VIDEO" && node.hasAttribute("poster")) {
          var rawPoster = node.getAttribute("poster");
          if (rawPoster && !isAlreadyPrefixed(rawPoster)) {
            var mappedPoster = toGatewayUrl(rawPoster);
            if (mappedPoster !== null) node.setAttribute("poster", mappedPoster.toString());
          }
        }
        if (tag === "IFRAME") injectIframe(node);
      } else if (tag === "LINK" || tag === "A") {
        if (tag === "LINK" && (node.rel === "manifest" || node.getAttribute("rel") === "manifest")) {
          if (!node.hasAttribute("crossorigin")) node.setAttribute("crossorigin", "use-credentials");
        }
        var rawHref = node.getAttribute("href");
        if (rawHref && !isAlreadyPrefixed(rawHref)) {
          var mapped = toGatewayUrl(rawHref);
          if (mapped !== null) {
            node.setAttribute("href", mapped.toString());
            if (node.href) node.href = mapped.toString();
          }
        }
      } else if (tag === "OBJECT") {
        var data = node.getAttribute("data") || node.data;
        if (data && !isAlreadyPrefixed(data)) {
          var mapped = toGatewayUrl(data);
          if (mapped !== null) node.setAttribute("data", mapped.toString());
        }
      } else if (tag === "FORM") {
        var action = node.getAttribute("action") || node.action;
        if (action && !isAlreadyPrefixed(action)) {
          var mapped = toGatewayUrl(action);
          if (mapped !== null) node.setAttribute("action", mapped.toString());
        }
      }
    };

    if (targetWindow.Element && targetWindow.Element.prototype.append) {
      var nativeAppend = targetWindow.Element.prototype.append;
      targetWindow.Element.prototype.append = function () {
        for (var i = 0; i < arguments.length; i++) rewriteElementNode(arguments[i]);
        return nativeAppend.apply(this, arguments);
      };
    }
    if (targetWindow.Node) {
      var nativeAppendChild = targetWindow.Node.prototype.appendChild;
      targetWindow.Node.prototype.appendChild = function (node) {
        rewriteElementNode(node);
        return nativeAppendChild.call(this, node);
      };
      var nativeInsertBefore = targetWindow.Node.prototype.insertBefore;
      targetWindow.Node.prototype.insertBefore = function (node, reference) {
        rewriteElementNode(node);
        return nativeInsertBefore.call(this, node, reference);
      };
    }

    if (targetWindow.MutationObserver) {
      var observer = new MutationObserver(function (mutations) {
        for (var i = 0; i < mutations.length; i++) {
          var nodes = mutations[i].addedNodes;
          for (var j = 0; j < nodes.length; j++) {
            if (nodes[j].tagName === "IFRAME") injectIframe(nodes[j]);
          }
        }
      });
      if (targetWindow.document && targetWindow.document.documentElement) {
        observer.observe(targetWindow.document.documentElement, { childList: true, subtree: true });
      }
    }

    // 拦截 SPA 路由 History API
    if (targetWindow.history) {
      var wrapHistory = function (orig) {
        if (!orig) return orig;
        return function (state, unused, url) {
          if (url) {
            var mapped = toGatewayUrl(url);
            if (mapped !== null) url = mapped.toString();
          }
          return orig.call(this, state, unused, url);
        };
      };
      targetWindow.history.pushState = wrapHistory(targetWindow.history.pushState);
      targetWindow.history.replaceState = wrapHistory(targetWindow.history.replaceState);
    }

    // 拦截 EventSource 流式连接
    if (targetWindow.EventSource) {
      var nativeEventSource = targetWindow.EventSource;
      targetWindow.EventSource = new Proxy(nativeEventSource, {
        construct: function (target, args, newTarget) {
          var mapped = toGatewayUrl(args[0]);
          if (mapped !== null) args = [mapped.toString()].concat(args.slice(1));
          return Reflect.construct(target, args, newTarget);
        }
      });
    }

    // 拦截 WebSocket 连接
    if (targetWindow.WebSocket) {
      var nativeWebSocket = targetWindow.WebSocket;
      var page = new URL(targetWindow.location.href);
      var pagePort = page.port || (page.protocol === "https:" ? "443" : "80");
      targetWindow.WebSocket = new Proxy(nativeWebSocket, {
        construct: function (target, args, newTarget) {
          var url;
          try { url = new URL(String(args[0]), targetWindow.location.href); }
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
    if (targetWindow.navigator && typeof targetWindow.navigator.sendBeacon === "function") {
      var nativeSendBeacon = targetWindow.navigator.sendBeacon.bind(targetWindow.navigator);
      targetWindow.navigator.sendBeacon = function (url, data) {
        var mapped = toGatewayUrl(url);
        return nativeSendBeacon(mapped !== null ? mapped.toString() : url, data);
      };
    }

    // 拦截 window.open
    if (typeof targetWindow.open === "function") {
      var nativeWindowOpen = targetWindow.open.bind(targetWindow);
      targetWindow.open = function (url, target, features) {
        var mapped = toGatewayUrl(url);
        return nativeWindowOpen(mapped !== null ? mapped.toString() : url, target, features);
      };
    }

    // 拦截 Worker 与 SharedWorker
    if (targetWindow.Worker) {
      var nativeWorker = targetWindow.Worker;
      targetWindow.Worker = new Proxy(nativeWorker, {
        construct: function (target, args, newTarget) {
          var mapped = toGatewayUrl(args[0]);
          if (mapped !== null) args = [mapped.toString()].concat(args.slice(1));
          return Reflect.construct(target, args, newTarget);
        }
      });
    }
    if (targetWindow.SharedWorker) {
      var nativeSharedWorker = targetWindow.SharedWorker;
      targetWindow.SharedWorker = new Proxy(nativeSharedWorker, {
        construct: function (target, args, newTarget) {
          var mapped = toGatewayUrl(args[0]);
          if (mapped !== null) args = [mapped.toString()].concat(args.slice(1));
          return Reflect.construct(target, args, newTarget);
        }
      });
    }
    if (targetWindow.navigator && targetWindow.navigator.serviceWorker && typeof targetWindow.navigator.serviceWorker.register === "function") {
      var nativeSWRegister = targetWindow.navigator.serviceWorker.register.bind(targetWindow.navigator.serviceWorker);
      targetWindow.navigator.serviceWorker.register = function (scriptURL, options) {
        var mapped = toGatewayUrl(scriptURL);
        if (mapped !== null) scriptURL = mapped.toString();
        if (options && options.scope) {
          var scopeMapped = toGatewayUrl(options.scope);
          if (scopeMapped !== null) options.scope = scopeMapped.pathname;
        }
        return nativeSWRegister(scriptURL, options);
      };
    }

    // DSH 客户端回环状态与配置持久化兼容补丁
    try{targetWindow.__DSH_TRANSPORT__=Object.assign(targetWindow.__DSH_TRANSPORT__||{},{ownsHost:true});}catch(_){}
    var hookModuleLoader = function (loader) {
      if (!loader || typeof loader.load !== "function" || loader.__hooked) return loader;
      var rawLoad = loader.load.bind(loader);
      loader.load = function (handoff) {
        if (handoff && handoff.id === "@deepseek-ai/dsh-client-connection" && typeof handoff.factory === "function") {
          var rawFactory = handoff.factory;
          handoff.factory = function () {
            var modExports = rawFactory.apply(this, arguments);
            if (modExports && typeof modExports.apply === "function") {
              var rawApply = modExports.apply;
              modExports.apply = function (ctx) {
                if (ctx && typeof ctx.provide === "function") {
                  var proxyCtx = new Proxy(ctx, {
                    get: function (target, prop, receiver) {
                      if (prop === "provide") {
                        return function (name, handle) {
                          if (name === "connection" && handle && typeof handle === "object") {
                            try {
                              Object.defineProperty(handle, "isLoopback", {
                                value: true,
                                writable: true,
                                configurable: true
                              });
                            } catch (_) {
                              handle.isLoopback = true;
                            }
                          }
                          return Reflect.apply(target.provide, target, arguments);
                        };
                      }
                      return Reflect.get(target, prop, receiver);
                    }
                  });
                  return rawApply.call(this, proxyCtx);
                }
                return rawApply.apply(this, arguments);
              };
            }
            return modExports;
          };
        }
        return rawLoad(handoff);
      };
      loader.__hooked = true;
      return loader;
    };

    if (targetWindow.__ModuleLoader__) {
      hookModuleLoader(targetWindow.__ModuleLoader__);
    } else {
      var storedLoader = undefined;
      try {
        Object.defineProperty(targetWindow, "__ModuleLoader__", {
          configurable: true,
          enumerable: true,
          get: function () { return storedLoader; },
          set: function (val) { storedLoader = hookModuleLoader(val); }
        });
      } catch (_) {}
    }
  };

  installBridge(window);
})("` + fnGatewayPrefix + `");
</script>`
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
		title = "服务构建中"
		desc = "正在同步依赖与编译运行环境"
		badgeClass = "badge-starting"
		badgeText = "构建中"
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
