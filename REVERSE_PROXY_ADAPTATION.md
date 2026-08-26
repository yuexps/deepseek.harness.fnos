# DeepSeek Harness 反向代理与网关适配技术文档

本文档记录了在飞牛 NAS 系统中，针对 **DeepSeek Harness (DSH)** 进行反向代理与网关子路径适配时所做的全部特殊处理与技术实现，供后续维护与版本升级参考。

---

## 一、设计目标与原则

- **零侵入原则**：完全不修改 DSH 上游源码（`deepseek-harness`），保持 DSH 核心代码的纯净性，方便后续直接升级。
- **全场景适配**：
  1. **飞牛网关模式**：子路径代理（`http://<NAS_IP>:5666/app/deepseek-harness/fngateway/`）；
  2. **独立代理模式**：独立端口（`https://<NAS_IP>:2299/`）；
  3. **本地回环模式**：本地调试（`http://127.0.0.1:2298/`）。
- **统一内聚**：所有适配逻辑统一收敛在 Go 反向代理服务层（[`proxy.go`](./proxy.go) 和 [`fngateway.go`](./fngateway.go)）。

---

## 二、核心问题与解决方案

| 序号 | 遇到的问题 / 现象 | 产生根因 | 处理方案 | 涉及文件 |
| :--- | :--- | :--- | :--- | :--- |
| **1** | 特权 API（配置读写、模型发现等）返回 **403 Forbidden** | DSH 后端通过 `isTrustedApiRequest` 判定请求头，特权接口只接受来自 `127.0.0.1` 且同源的请求 | 在 Go 反向代理的 `Rewrite` 阶段，将发送给 DSH 后端的请求头改写为目标同源 `Host`、`Origin`，并将 `Sec-Fetch-Site` 设为 `same-origin` | `proxy.go`<br>`fngateway.go` |
| **2** | 飞牛网关子路径下静态资源与接口 **404** | DSH 前端默认为根路径 `/` 构建，子路径反代下静态标签与动态请求未带网关前缀 | 1. 代理响应 HTML 时正则替换静态属性（`src`/`href`）；<br>2. 注入 `fnGatewayBridgeScript` 拦截 `fetch`、`XHR`、`WebSocket`、`EventSource`、DOM 插入等动态请求 | `fngateway.go` |
| **3** | HTTP 局域网访问时控制台报错 `randomUUID is not a function` | 现代浏览器将 `crypto.randomUUID()` 限制在安全上下文（HTTPS / localhost），普通 HTTP IP 无法使用 | 在 HTML 头部注入纯 JS 实现的 RFC4122 v4 UUID 生成器，Polyfill 到 `window.crypto` | `proxy.go`<br>`fngateway.go` |
| **4** | 反代下「插件配置」面板空白、模型设置无法读取保存 | DSH 客户端 `@deepseek-ai/dsh-client-connection` 依 `location.hostname` 判定 `isLoopback`；非 127.0.0.1 时将配置模式置为 `'memory'` 并拒绝向后端拉取数据 | 1. 注入脚本 Hook `window.__ModuleLoader__`，在注册 `connection` 服务时将 `handle.isLoopback` 置为 `true`；<br>2. 代理转发 `.js` 时通过 `rewriteJsBundle` 兜底替换 | `proxy.go`<br>`fngateway.go` |
| **5** | 远程 Web 访问时右上角显示红字“无法打开配置文件” | 官方设计中该按钮会调用桌面 GUI 编辑器（如 `xdg-open`），在 Linux 无头 NAS 服务器上无法执行 | 注入 `<style>[data-slot="settings.action"] { display: none !important; }</style>`，隐藏无头环境下无意义的桌面级操作 | `proxy.go`<br>`fngateway.go` |

---

## 三、具体技术实现详解

### 1. 反代标头伪装（绕过特权回环安全栅栏）
在 Go 的 `httputil.ReverseProxy.Rewrite` 回调中执行：
```go
// 改写为目标同源 Origin，防止上游 CSRF 校验失败并保留特权访问能力
if pr.Out.Header.Get("Origin") != "" {
    pr.Out.Header.Set("Origin", fmt.Sprintf("%s://%s", proxyTarget.Scheme, proxyTarget.Host))
}
// 改写为 same-origin，防止跨站/iframe 标记被上游拦截
if pr.Out.Header.Get("Sec-Fetch-Site") != "" {
    pr.Out.Header.Set("Sec-Fetch-Site", "same-origin")
}
// 禁用压缩以便代理层进行响应注入与改写
pr.Out.Header.Set("Accept-Encoding", "identity")
```

### 2. 客户端模块加载器 Hook（启用配置读写与插件卡片）
在注入到 HTML `<head>` 的脚本中，拦截 Cordis 模块系统：
```javascript
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

if (window.__ModuleLoader__) {
  hookModuleLoader(window.__ModuleLoader__);
} else {
  var storedLoader = undefined;
  try {
    Object.defineProperty(window, "__ModuleLoader__", {
      configurable: true,
      enumerable: true,
      get: function () { return storedLoader; },
      set: function (val) { storedLoader = hookModuleLoader(val); }
    });
  } catch (_) {}
}
```

### 3. 网关全同源路由拦截器（子路径无感路由与防逃逸）
在 [`fngateway.go`](./fngateway.go) 中，通过全维度拦截封堵前端全部网络与渲染通道：
- **`window.fetch`**：自动改写同源绝对路径 URL（支持 Request 实例与字符串，具备容错降级）；
- **`XMLHttpRequest.prototype.open`**：重写 Ajax 请求路径；
- **`HTMLImageElement` / `HTMLMediaElement` / `HTMLSourceElement` 等原型 Property Setter**：拦截图片与媒体资源的 `src`、`srcset` 等赋值，解决赋值瞬间即刻触发原生网络请求的问题；
- **`<script>` 脚本精准挂载拦截**：保持 `HTMLScriptElement.prototype.src` 原生状态，在 `appendChild` / `insertBefore` 挂载 DOM 时单次改写，确保与 DSH `client-modules` 模块加载器的注册时序严格同步；
- **`Element.prototype.setAttribute` / `setAttributeNS`**：精准重写动态设置的 `src`、`href`、`srcset` 等属性（`data` 属性限定 `<object>` 标签，防止误伤业务属性）；
- **`Element.prototype.innerHTML` / `insertAdjacentHTML`**：正则解析替换 HTML 字符串内的相对资源路径；
- **`history.pushState` / `replaceState`**：防止 SPA 路由跳转覆盖子路径前缀导致刷新 404；
- **同源 `<iframe>` 穿透挂载**：动态挂载的同源 iframe 自动递归初始化拦截桥接脚本；
- **`window.WebSocket` / `EventSource`**：重写 WebSocket 握手与 SSE 流式连接路径；
- **`Worker` / `SharedWorker` / `serviceWorker.register`**：改写多线程与后台服务脚本路径及 scope；
- **`manifest.webmanifest` 子路径动态适配**：动态解析并改写 PWA 清单中的 `scope`、`start_url`、`id` 及图标路径，确保在子路径反代下 PWA 语法与作用域校验 100% 吻合；
- **`Set-Cookie` 响应头作用域改写**：将根路径 Cookie 映射至网关子路径下。

### 4. 样式优化（清理无头环境下无效控件）
在 HTML 注入脚本头部注入：
```html
<style>[data-slot="settings.action"] { display: none !important; }</style>
```
隐藏原版仅能在本地图形桌面环境下使用的“打开配置文件”按钮，避免在 Linux 服务器下报出无头错误。

---

## 四、维护与排查指南

### 1. 验证插件配置与模型配置是否正常
- 在浏览器打开设置页面，F12 控制台执行：
  ```javascript
  fetch('/api/settings.describe', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ type: 'client-request', rpcId: 'test', method: 'settings.describe', payload: {} })
  }).then(r => r.json()).then(console.log);
  ```
  预期结果：返回 `ok: true`，且包含 `shell`、`agent-loop`、`web-search-deepseek` 等命名空间。

### 2. DSH 上游版本升级时的注意事项
- 如果升级 DSH 版本，只需替换源码，**无需重新为前端打 patch**；
- 升级后只需检查 DSH 的 `connection` 服务名是否有重大架构变动即可。
