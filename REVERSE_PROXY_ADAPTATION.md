# DeepSeek Harness 反向代理与网关适配技术文档

本文档记录适配最新版 **DeepSeek Harness (DSH v0.1.7+)** 反向代理与网关子路径的技术方案与实现细节。

---

## 一、设计原则

1. **前缀剥离反代**：遵循官方前缀剥离代理规范，配合前端原生 `<base href="./">` 解析相对路径；
2. **零侵入上游**：不修改官方 NPM 运行时（`@deepseek-ai/dsh`），保持上游纯净；
3. **多入口适配**：统一支持飞牛网关子路径（`/app/deepseek-harness/fngateway/`）、独立反代端口（`:2299`）与本地回环（`:2298`）；
4. **统一收敛**：所有标头改写、会话代持与补丁注入统一在 Go 代理层（`proxy.go`、`fngateway.go`、`config.go`）处理。

---

## 二、问题排查与解决对照表

| 序号 | 现象 / 问题 | 根本原因 | 解决方案 | 涉及文件 |
| :--- | :--- | :--- | :--- | :--- |
| **1** | 特权 API（配置读写、模型发现）报 **403 Forbidden** | 后端仅允许来自本地回环且同源的请求 | `Rewrite` 阶段重写 `Host`、`Origin` 为后端回环地址，设置 `Sec-Fetch-Site: same-origin` | `proxy.go`<br>`fngateway.go` |
| **2** | 网关裸路径（`/fngateway`）访问导致静态资源及 API **404** | 未带斜杠时 `<base href="./">` 会导致浏览器基准目录上升至父级 | 增加末尾斜杠强制规范化，访问 `/fngateway` 自动 301 重定向至 `/fngateway/` | `fngateway.go` |
| **3** | HTTP 局域网访问报错 `randomUUID is not a function` | 浏览器限制 `crypto.randomUUID()` 仅在安全上下文可用 | 上游原生已改用全环境兼容的 `crypto.getRandomValues`，代理层无需注入 polyfill | 上游源码 |
| **4** | 远程访问下插件配置面板空白、无法读取保存设置 | 前端仅当回环地址时才向后端请求配置，非回环默认降级为 memory 模式 | 注入 `window.__DSH_TRANSPORT__ = { ownsHost: true }`，启用原生宿主特权分支 | `proxy.go`<br>`fngateway.go` |
| **5** | 页面右上角常驻红字报错“无法打开配置文件” | 前端尝试调用无头（Headless）环境缺失的图形化桌面编辑器（`xdg-open`） | 注入 CSS 隐藏该按钮：`[data-slot="settings.action"] { display: none !important; }` | `proxy.go`<br>`fngateway.go` |
| **6** | 会话头部出现桌面应用分体按钮（如“在 Zed 中打开”） | 后端未检测到 SSH 环境变量时向前端下发本地已安装桌面应用列表 | `InitAppEnv()` 注入 `SSH_CONNECTION="127.0.0.1 0 127.0.0.1 22"`，标记为远程无头环境 | `config.go` |
| **7** | 移动端 App 提示鉴权失效或报错 `HTML did not preload client.js` | 移动端 WebView 无法自动携带 Strict Cookie、无防缓存头导致缓存旧资源、连续问号折叠 | 1. 服务端代持 `dsh-auth-*` Cookie 并自动注入转发；<br>2. HTML 响应强制 `Cache-Control: no-store`；<br>3. 转发前自动还原 `/plugins/?/` 为 `/plugins/??/` | `harness.go`<br>`proxy.go`<br>`fngateway.go` |
| **8** | 子路径下附件上传与 WebSocket 连接脱落前缀 | 旧版本采用绝对路径拼接导致脱落挂载前缀 | 上游原生基于 `document.baseURI` 解析，代理层保持前缀剥离与 Cookie Path 改写即可 | `fngateway.go` |

---

## 三、核心技术实现细节

### 1. 反向代理标头改写
改写请求标头通过 DSH 本地特权校验，并关闭代理层压缩以支持 HTML 注入：
```go
pr.Out.Header.Set("Host", targetURL.Host)
pr.Out.Header.Set("Origin", "http://"+targetURL.Host)
pr.Out.Header.Set("Sec-Fetch-Site", "same-origin")
pr.Out.Header.Set("Accept-Encoding", "identity")
```

### 2. 裸路径末尾斜杠强制规范化
确保单页应用基准路径解析正确：
```go
if c.Request.URL.Path == fnGatewayPrefix {
	target := fnGatewayPrefix + "/"
	if c.Request.URL.RawQuery != "" {
		target += "?" + c.Request.URL.RawQuery
	}
	c.Redirect(http.StatusMovedPermanently, target)
	return
}
```

### 3. 统一补丁注入（httpPolyfillScript）
向 HTML 注入纯净轻量的样式与特权声明（共 1 行）：
```html
<style>[data-slot="settings.action"] { display: none !important; }</style><script>try{window.__DSH_TRANSPORT__=Object.assign(window.__DSH_TRANSPORT__||{},{ownsHost:true});}catch(_){}</script>
```

### 4. 远程环境标记注入
在 [`config.go`](./config.go) 初始化中注入环境变量，使后端自动关闭桌面探测并降级为 Web 选择器：
```go
_ = os.Setenv("SSH_CONNECTION", "127.0.0.1 0 127.0.0.1 22")
```

### 5. 服务端会话代持与契约容错
- **会话代持**：捕获客户端 Token，由 Go 服务端直接换取并持有 `dsh-auth-*` Cookie，转发至上游时统一补齐；
- **禁用缓存**：HTML 响应头强制设置 `Cache-Control: no-store, no-cache, must-revalidate`；
- **问号还原**：对形如 `/plugins/?/...` 请求还原为 `/plugins/??/...`；
- **Cookie Path 改写**：网关模式下通过正则将 `Path=/` 改写为 `Path=/app/deepseek-harness/fngateway/`。

---

## 四、排查与验证方法

### 1. 验证特权 API 状态
在控制台验证特权通道是否打通（应返回配置信息且 `ok: true`）：
```javascript
fetch('api/settings.describe', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ type: 'client-request', rpcId: 'test', method: 'settings.describe', payload: {} })
}).then(r => r.json()).then(console.log);
```

### 2. 验证基准路径与 WebSocket
- 检查基准路径（应包含网关完整前缀）：
  ```javascript
  console.log('Document Base URI:', document.baseURI);
  ```
- 检查网络面板 `/api/remote.mux` WebSocket 连接，状态应为 `101 Switching Protocols`。

### 3. 上游升级排查要点
- 更新 DSH 核心包无需重新编译前端；
- 确认上游 `ownsHost` 声明字段及 RPC 路由无破坏性变更；
- 确保代理层继续保持裸路径末尾斜杠重定向规范化。
