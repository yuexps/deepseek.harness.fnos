# DeepSeek Harness 反向代理与网关适配技术文档

本文档记录了在飞牛 NAS 系统中，针对 **DeepSeek Harness (DSH)** 进行反向代理与网关子路径适配时所做的全部特殊处理与技术实现，供后续维护与版本升级参考。

---

## 一、设计目标与原则

- **零侵入原则**：完全不修改 DSH 官方 NPM 运行时（`@deepseek-ai/dsh`），保持 DSH 核心代码的纯净性，方便后续直接升级。
- **全场景适配**：
  1. **飞牛网关模式**：子路径代理（`http://<NAS_IP>:5666/app/deepseek-harness/fngateway/`）；
  2. **独立代理模式**：独立端口（`http://<NAS_IP>:2299/`）；
  3. **本地回环模式**：本地调试（`http://127.0.0.1:2298/`）。
- **统一内聚**：所有适配逻辑统一收敛在 Go 反向代理服务层（[`proxy.go`](./proxy.go) 和 [`fngateway.go`](./fngateway.go)）。

---

## 二、核心问题与解决方案

| 序号 | 遇到的问题 / 现象 | 产生根因 | 处理方案 | 涉及文件 |
| :--- | :--- | :--- | :--- | :--- |
| **1** | 特权 API（配置读写、模型发现等）返回 **403 Forbidden** | DSH 后端通过 `isTrustedApiRequest` 判定请求头，特权接口只接受来自 `127.0.0.1` 且同源的请求 | 在 Go 反向代理的 `Rewrite` 阶段，将发送给 DSH 后端的请求头改写为目标同源 `Host`、`Origin`，并将 `Sec-Fetch-Site` 设为 `same-origin` | `proxy.go`<br>`fngateway.go` |
| **2** | 飞牛网关子路径下静态资源与接口 **404** | DSH 前端默认为根路径 `/` 构建，子路径反代下静态标签、动态请求及 WebSocket 复用流未带网关前缀 | 1. 代理响应 HTML 时正则替换静态属性（`src`/`href`）；<br>2. 注入 `fnGatewayBridgeScript` 拦截 `fetch`、`XHR`、`WebSocket`（含 `/api/remote.mux`）、`EventSource`、DOM 插入等动态请求 | `fngateway.go` |
| **3** | HTTP 局域网访问时控制台报错 `randomUUID is not a function` | 现代浏览器将 `crypto.randomUUID()` 限制在安全上下文（HTTPS / localhost），普通 HTTP IP 无法使用 | 在 HTML 头部注入纯 JS 实现的 RFC4122 v4 UUID 生成器，Polyfill 到 `window.crypto`（上游已引入 `@deepseek-ai/dsh-util-crypto`，此处作为全环境兜底防御） | `proxy.go`<br>`fngateway.go` |
| **4** | 反代下「插件配置」面板空白、模型设置无法读取保存 | DSH 客户端 `@deepseek-ai/dsh-client-connection` 依 `location.hostname` 判定 `isLoopback`；非 127.0.0.1 时将配置模式置为 `'memory'` 并拒绝向后端拉取数据 | **双重安全防护机制**：<br>1. 注入 `window.__DSH_TRANSPORT__ = { ownsHost: true }` 走通上游原生特权分支；<br>2. Hook `window.__ModuleLoader__`，在注册 `connection` 服务时劫持 `handle.isLoopback = true`（保持 JS 产物 100% 原始纯净，不进行暴力文本替换） | `proxy.go`<br>`fngateway.go` |
| **5** | 远程 Web 访问时右上角显示红字“无法打开配置文件” | 官方设计中该按钮会调用桌面 GUI 编辑器（如 `xdg-open`），在 Linux 无头 NAS 服务器上无法执行 | 注入 `<style>[data-slot="settings.action"] { display: none !important; }</style>`，隐藏无头环境下无意义的桌面级操作 | `proxy.go`<br>`fngateway.go` |
| **6** | 会话头部出现“在 Zed 中打开工作目录”等无效分体按钮 | DSH 上游新增 `open-in-app` 桌面级功能；因后台守护进程无 SSH 标记，DSH 误判为本地个人电脑并探测本地应用渲染了启动按钮 | 在全局环境初始化 `InitAppEnv()` 时注入 `SSH_CONNECTION=127.0.0.1 0 127.0.0.1 22`，触发 DSH 原生远程无头环境模式，应用列表自动置空并隐藏该按钮 | `config.go` |
| **7** | 飞牛安卓 App 提示鉴权失败或报 `HTML did not preload client.js` | 1. 官方 Cookie 为 `SameSite=Strict`，移动端 WebView 无法留存凭据导致后续 401，原反代无条件拦截 401 易形成循环重定向；<br>2. 官方 HTML 缺少缓存头导致 WebView 强缓存旧 `rev` 触发 404；<br>3. 移动端网络栈对 `/plugins/??` 连续问号的折叠引发上游精确路由失配 | **会话服务端代持与启动契约全链路兜底**：<br>1. **会话代持**：反代捕获令牌，服务端内存统一向回环换票持有 `dsh-auth-*`，转发自动注入并剥离客户端 Cookie（彻底解耦对客户端凭据的依赖，遇 401 仅失效服务端缓存，移除客户端拦截重定向以杜绝死循环）；<br>2. **禁止强缓存**：HTML 响应强制注入 `Cache-Control: no-store, no-cache, must-revalidate`，杜绝旧启动契约被复用；<br>3. **问号容错还原**：发往上游前自动检测并补齐被中间层折叠丢失的 `/plugins/??` 首个问号，100% 保持官方原版路由契约成立 | `harness.go`<br>`proxy.go`<br>`fngateway.go` |

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

### 2. 客户端特权状态注入与模块加载器 Hook（启用配置读写与插件卡片）
结合 DSH 最新架构演进，采用**原生协议声明 + 运行时模块 Hook**双重安全体系（完全避免对 bundle 字节码进行暴力文本替换，防止破坏 JS 语法树）：
```javascript
// 1. 原生协议声明：上游 client-connection 在检测到 ownsHost 时将 isLoopback 初始化为 true
try {
  window.__DSH_TRANSPORT__ = Object.assign(window.__DSH_TRANSPORT__ || {}, { ownsHost: true });
} catch (_) {}

// 2. 运行时 Hook：拦截 Cordis 模块加载，劫持 connection 句柄
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
- **`<base href="...">` 标签注入**：自动在 HTML 头部插入子路径基准，对齐相对路径资源解析基准；
- **`Location.prototype` 原型拦截**：重写 `pathname` Getter/Setter（读取时透明剥除前缀，设置时自动补全）以及 `assign` / `replace` 编程式跳转，防止路由状态误判与跳转逃逸；
- **`window.fetch`**：自动改写同源绝对路径 URL（支持 Request 实例与字符串，具备容错降级）；
- **`XMLHttpRequest.prototype.open`**：重写 Ajax 请求路径；
- **`HTMLImageElement` / `HTMLVideoElement` / `HTMLMediaElement` / `HTMLSourceElement` 等原型 Property Setter**：拦截图片与媒体资源的 `src`、`srcset`、`poster` 等赋值，解决赋值瞬间即刻触发原生网络请求的问题；
- **`<script>` 脚本精准挂载拦截**：保持 `HTMLScriptElement.prototype.src` 原生状态，在 `appendChild` / `insertBefore` 挂载 DOM 时单次改写，确保与 DSH `client-modules` 模块加载器的注册时序严格同步；
- **`Element.prototype.setAttribute` / `setAttributeNS`**：精准重写动态设置的 `src`、`href`、`action`、`poster`、`srcset` 等属性（`data` 属性限定 `<object>` 标签，防止误伤业务属性）；
- **`Element.prototype.innerHTML` / `insertAdjacentHTML`**：正则解析替换 HTML 字符串内的相对资源路径（支持 `src`、`href`、`action`、`poster`）；
- **`history.pushState` / `replaceState`**：防止 SPA 路由跳转覆盖子路径前缀导致刷新 404；
- **同源 `<iframe>` 穿透挂载**：动态挂载的同源 iframe 自动递归初始化拦截桥接脚本；
- **`window.WebSocket` / `EventSource`**：重写 WebSocket 握手（包含最新的 `/api/remote.mux` 复用流通道）与 SSE 流式连接路径；
- **`Worker` / `SharedWorker` / `serviceWorker.register`**：改写多线程与后台服务脚本路径及 scope；
- **`manifest.webmanifest` 子路径动态适配**：动态解析并改写 PWA 清单中的 `scope`、`start_url`、`id` 及图标路径，确保在子路径反代下 PWA 语法与作用域校验 100% 吻合；
- **`Set-Cookie` 响应头作用域改写**：将根路径 Cookie 映射至网关子路径下。

### 4. 样式优化（清理无头环境下无效控件）
在 HTML 注入脚本头部注入：
```html
<style>[data-slot="settings.action"] { display: none !important; }</style>
```
隐藏原版仅能在本地图形桌面环境下使用的“打开配置文件”按钮，避免在 Linux 服务器下报出无头错误。

### 5. 远程运行环境声明（消除 Open In 按钮与无头环境桌面弹窗）
在 `config.go` 的全局环境初始化 `InitAppEnv()` 中统一注入环境变量：
```go
_ = os.Setenv("SSH_CONNECTION", "127.0.0.1 0 127.0.0.1 22")
```
- **核心机制**：DSH 内部通过 `launchedThroughSsh(launchEnvironmentOf(ctx))` 判定是否存在 `SSH_CONNECTION` 或 `SSH_TTY`；
- **效果对齐**：
  1. `@deepseek-ai/dsh-host-open-in-app` 检测到 SSH 远程标记后，探测结果返回空映射 `new Map()`，前端 `@deepseek-ai/dsh-client-ui-open-in-app` 自动销毁分体胶囊按钮，不占用任何 DOM；
  2. 自动切换目录选择器为适用于远程 Web 的 `browse` 模式，杜绝在无头 Linux 上调用 `zenity`/`kdialog` 导致异常；
  3. 彻底禁用后台进程在无头服务器上尝试打开本地浏览器的行为。

### 6. 服务端会话代持与模块加载契约对齐
针对移动端 WebView 环境凭据隔离与请求折叠问题：
- **服务端内存代持会话**：反代层捕获启动令牌，由 Go 守护进程在内存中统一向本地换取官方 `dsh-auth-*` 会话凭据并注入转发请求，彻底解耦对客户端 Cookie 的依赖；遇 401 仅失效内存缓存，移除客户端拦截重定向以杜绝循环重定向；
- **禁用 HTML 强缓存**：拦截 HTML 响应并注入 `Cache-Control: no-store, no-cache, must-revalidate`，防止 WebView 缓存旧版 `rev` 静态资源契约导致 404；
- **问号容错还原**：反代在转发 `/plugins/??` 批量加载请求前，自动检测并还原被网络栈折叠的首个问号，确保上游原生模块加载契约成立。

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
- 如果升级 DSH 版本，只需在线更新或替换 NPM 离线包，**无需重新为前端打 patch**；
- 升级后只需检查 DSH 的 `connection` 服务名是否有重大架构变动即可。
