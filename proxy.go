package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/soheilhy/cmux"
)

//go:embed templates/auth_login.html
var authLoginPageTplContent string

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
	proxyHTTPS  *http.Server
	proxyCmux   cmux.CMux
	proxyTarget *url.URL
	proxyAddr   string
	proxyTLS    *tls.Config
)

func updateReverseProxyTarget() {
	cfg := GetConfig()

	port := cfg.ServerPort
	if port <= 0 {
		port = 2298
	}
	proxyTarget, _ = url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))

	proxyPort := cfg.ProxyPort
	if proxyPort <= 0 {
		proxyPort = 2299
	}
	proxyAddr = fmt.Sprintf("0.0.0.0:%d", proxyPort)
}

func listAllDNSNames() []string {
	names := []string{"localhost", "deepseek-harness"}
	if hostname, err := os.Hostname(); err == nil && hostname != "" && hostname != "localhost" {
		names = append(names, hostname)
	}
	return names
}

func generateSelfSignedCert(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("生成 ECDSA 密钥失败: %s", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("生成序列号失败: %s", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"DeepSeek Harness"},
			CommonName:   "deepseek-harness",
		},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              listAllDNSNames(),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("创建证书失败: %s", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("写入证书文件失败: %s", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("PEM 编码证书失败: %s", err)
	}

	keyOut, err := os.Create(keyPath)
	if err != nil {
		return fmt.Errorf("写入密钥文件失败: %s", err)
	}
	defer keyOut.Close()
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("序列化私钥失败: %s", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("PEM 编码私钥失败: %s", err)
	}

	LogInfo("TLS 自签名证书已就绪: %s", certPath)
	return nil
}

func loadOrCreateProxyTLS() (*tls.Config, error) {
	autoDir := globalPkgVar
	if autoDir == "" {
		autoDir = "."
	}
	certFile := filepath.Join(autoDir, "harness.crt")
	keyFile := filepath.Join(autoDir, "harness.key")

	needRegen := false
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		needRegen = true
	} else if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		needRegen = true
	}

	if needRegen {
		if err := generateSelfSignedCert(certFile, keyFile); err != nil {
			return nil, fmt.Errorf("生成自签名证书失败: %s", err)
		}
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("加载 TLS 证书失败: %s", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func startReverseProxy() error {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	return startReverseProxyLocked()
}

func startReverseProxyLocked() error {
	if proxyHTTP != nil || proxyHTTPS != nil {
		return nil
	}

	updateReverseProxyTarget()

	tlsCfg, err := loadOrCreateProxyTLS()
	if err != nil {
		LogWarning("TLS 证书加载失败，反向代理未启动: %s", err)
		return err
	}
	proxyTLS = tlsCfg

	errHandler := func(w http.ResponseWriter, r *http.Request, err error) {
		// 过滤客户端主动断开连接/取消请求的正常行为
		if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			return
		}
		LogWarning("反向代理转发错误 [%s]: %s", proxyAddr, err)
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
		},
		ModifyResponse: func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))

			// 处理 SSE 流式响应标头
			if strings.HasPrefix(contentType, "text/event-stream") {
				resp.Header.Set("Cache-Control", "no-cache, no-transform")
				resp.Header.Set("X-Accel-Buffering", "no")
				resp.Header.Del("Content-Length")
				return nil
			}

			// 拦截 HTML 注入 Polyfill 修复非安全上下文环境
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
			}

			// 拦截 JS 资源改写回环状态判定
			if (strings.Contains(contentType, "javascript") || strings.Contains(contentType, "text/javascript")) && resp.Body != nil {
				bodyBytes, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return err
				}

				modified := rewriteJsBundle(bodyBytes)
				resp.Body = io.NopCloser(bytes.NewReader(modified))
				resp.ContentLength = int64(len(modified))
				resp.Header.Set("Content-Length", strconv.Itoa(len(modified)))
			}

			return nil
		},
		ErrorHandler: errHandler,
	}

	// 建立 TCP 监听器
	ln, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		LogWarning("反向代理端口监听失败 [%s]: %s", proxyAddr, err)
		return err
	}

	// cmux 协议分发
	mx := cmux.New(ln)
	tlsL := mx.Match(cmux.TLS())
	httpL := mx.Match(cmux.Any())

	proxyCmux = mx
	proxyHTTPS = &http.Server{Handler: proxyWithAuth(proxy), TLSConfig: tlsCfg}
	proxyHTTP = &http.Server{Handler: proxyWithAuth(proxy)}

	LogInfo("Web 服务就绪探测通过，反向代理启动完成 [%s → %s]", proxyAddr, proxyTarget.String())

	go func() {
		if err := proxyHTTPS.ServeTLS(tlsL, "", ""); err != nil && !isExpectedCloseErr(err) {
			LogWarning("HTTPS 代理服务异常退出: %s", err)
		}
	}()
	go func() {
		if err := proxyHTTP.Serve(httpL); err != nil && !isExpectedCloseErr(err) {
			LogWarning("HTTP 代理服务异常退出: %s", err)
		}
	}()
	go func() {
		if err := mx.Serve(); err != nil && !isExpectedCloseErr(err) {
			LogWarning("cmux 协议多路复用器退出: %s", err)
		}
	}()

	return nil
}

func isExpectedCloseErr(err error) bool {
	if err == nil || err == http.ErrServerClosed || err == net.ErrClosed || err == cmux.ErrListenerClosed || err == cmux.ErrServerClosed || errors.Is(err, context.Canceled) {
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
	if proxyHTTP == nil && proxyHTTPS == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if proxyHTTPS != nil {
		_ = proxyHTTPS.Shutdown(ctx)
		proxyHTTPS = nil
	}
	if proxyHTTP != nil {
		_ = proxyHTTP.Shutdown(ctx)
		proxyHTTP = nil
	}
	if proxyCmux != nil {
		proxyCmux.Close()
		proxyCmux = nil
	}
	LogInfo("反向代理服务已停止")
}

// proxyErrMessage 根据当前服务状态给出准确的代理错误提示
func proxyErrMessage() string {
	switch state.Status() {
	case StatusStarting:
		return "服务启动中，请稍候重试"
	case StatusRunning:
		return "服务响应异常，请检查后端运行状态"
	case StatusBuilding:
		return "服务构建中，请稍候重试"
	case StatusStopped:
		return "服务未启动，请在概览页启动"
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
	LogInfo("反向代理配置已变更，执行热重载")
	if err := startReverseProxyLocked(); err != nil {
		LogWarning("反向代理热重载失败: %s", err)
	}
}

const httpPolyfillScript = `<style>[data-slot="settings.action"] { display: none !important; }</style><script>(function(){
  var c=window.crypto;
  if(c&&typeof c.randomUUID!=="function"&&typeof c.getRandomValues==="function"){
    var getRand=c.getRandomValues.bind(c);
    var uuid=function(){
      var b=new Uint8Array(16);
      getRand(b);
      b[6]=(b[6]&15)|64;
      b[8]=(b[8]&63)|128;
      var h=Array.from(b,function(x){return("0"+x.toString(16)).slice(-2);}).join("");
      return h.slice(0,8)+"-"+h.slice(8,12)+"-"+h.slice(12,16)+"-"+h.slice(16,20)+"-"+h.slice(20);
    };
    var install=function(target){
      try{Object.defineProperty(target,"randomUUID",{configurable:true,writable:true,value:uuid});return typeof target.randomUUID==="function";}catch(_){return false;}
    };
    if(!install(c)&&Object.getPrototypeOf(c))install(Object.getPrototypeOf(c));
  }

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
                var rawProvide = ctx.provide.bind(ctx);
                ctx.provide = function (name, handle) {
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
                  return rawProvide(name, handle);
                };
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
  if (window.__ModuleLoader__) {
    hookModuleLoader(window.__ModuleLoader__);
  } else {
    var storedLoader = undefined;
    try {
      Object.defineProperty(window, "__ModuleLoader__", {
        configurable: true,
        enumerable: true,
        get: function () { return storedLoader; },
        set: function (val) {
          storedLoader = hookModuleLoader(val);
        }
      });
    } catch (_) {}
  }
})();</script>`

// rewriteJsBundle 改写客户端 JS 中的回环判断，使远程/反代访问时也能正常读取并持久化插件设置
func rewriteJsBundle(body []byte) []byte {
	res := bytes.ReplaceAll(body, []byte(`connection.isLoopback ? "host" : "memory"`), []byte(`"host"`))
	res = bytes.ReplaceAll(res, []byte(`connection.isLoopback ? 'host' : 'memory'`), []byte(`'host'`))
	res = bytes.ReplaceAll(res, []byte(`connection.isLoopback?"host":"memory"`), []byte(`"host"`))
	res = bytes.ReplaceAll(res, []byte(`connection.isLoopback?'host':'memory'`), []byte(`'host'`))
	return res
}

// injectHtmlPolyfill 将兼容补丁注入 HTML 的 head 头部
func injectHtmlPolyfill(body []byte) []byte {
	lower := bytes.ToLower(body)
	idx := bytes.Index(lower, []byte("<head"))
	if idx != -1 {
		closeIdx := bytes.IndexByte(lower[idx:], '>')
		if closeIdx != -1 {
			insertPos := idx + closeIdx + 1
			var res bytes.Buffer
			res.Grow(len(body) + len(httpPolyfillScript))
			res.Write(body[:insertPos])
			res.WriteString(httpPolyfillScript)
			res.Write(body[insertPos:])
			return res.Bytes()
		}
	}
	var res bytes.Buffer
	res.Grow(len(body) + len(httpPolyfillScript))
	res.WriteString(httpPolyfillScript)
	res.Write(body)
	return res.Bytes()
}

