# 0004：Model Gateway 子路径部署方案

## 状态与目标

状态：最小实现已落地，接口与自动化回归已验证；浏览器点击验收受来源权限设置阻塞，尚未完成。

本需求中的 modelgw 指部署入口 `/modelgw`，不是更换项目名称，也不要求迁移后端语言。保留 New API 的 Go 后端、React 前端及现有功能，通过一个服务提供完整子路径部署。

成功标准：控制台、管理 API、模型中转、MCP 和已有任务能力均通过 `/modelgw` 正常工作；登录刷新、回调和生成链接不逃逸到根路径。原有鉴权、权限、计费和协议语义不因路径调整发生变化。

## 方案决策

采用“应用入口挂载子路径，内部业务路由保持原样”的方案。固定 `/modelgw`，不增加动态多前缀、根路径别名或额外代理服务。

外部代理挂载适合已经有统一网关的部署，但不能消除前端、Cookie 和公开 URL 的适配。当前以单容器直接使用为约束，选择应用内挂载。若后续部署在代理后，代理必须保留此前缀，避免重复剥离。

不逐个修改 Gin RouterGroup，不使用响应正文搜索替换，也不建立自动重写所有响应 URL 的通用中间件。

## 路径契约

| 场景 | 外部地址或行为 |
| --- | --- |
| 控制台 | `/modelgw/`，页面深链接在此前缀下刷新可用 |
| 管理 API | `/modelgw/api/...` |
| 模型接口 | `/modelgw/v1/...`、`/modelgw/v1beta/...` 等已有协议入口 |
| MCP 和任务 | `/modelgw/mcp/...` 及其他已有任务入口 |
| 裸前缀 | `/modelgw` 以 308 跳转到 `/modelgw/`，保留查询参数和请求方法 |
| 未带前缀 | 应用端返回 404，包括 `/`、`/api/...`、`/v1/...` |
| 相似前缀 | `/modelgw-other` 不匹配 |

根路径 404 是部署约束，不代替身份认证或网络访问控制。同域名其他系统由外部入口分流，不交给本应用处理。

## 后端入口

在 HTTP Server 与现有 Gin Engine 之间匹配路径边界，优先使用标准库 `http.StripPrefix` 剥离前缀。确认实际 Gin 行为对 `Path`、`RawPath` 与 `RequestURI` 的依赖，只补必要的请求适配，保留查询参数和正确的编码语义。

入口不缓存请求体、不接管响应体，保留 SSE 刷新、WebSocket 升级、取消传播和连接释放能力。

Gin 已支持 `X-Forwarded-Prefix` 自动生成带前缀的尾斜杠跳转，入口固定覆盖该头，避免客户端指定错误前缀。该行为已有集成测试验证，无需包装 ResponseWriter 或重写 `Location`。

## 公开地址与回调

复用现有 `ServerAddress`，将其定义为完整公开应用地址，例如 `https://example.com/modelgw`，不另设一套域名来源。默认地址包含此前缀；正式部署时由管理员明确配置实际地址，不自动补写或迁移旧配置，不增加双路径处理。

实施前按实际用途梳理已有 URL 构造入口：OAuth、密码找回邮件、支付返回与通知、任务媒体和下载、MCP 展示链接。扩展已有 URL 构造能力，避免每个业务重复拼接。对独立配置的支付、任务或前端公开地址，保留其既有优先级，不盲目强制追加本应用前缀。

OAuth 注册回调及支付平台通知地址同步更新。前端 OAuth 授权参数与后端 token 交换必须使用一致的回调地址。Passkey 的 Origin 仍是协议、主机及端口，RP ID 仍是域名，均不能加入路径。

## Cookie 与登录流程

刷新 Cookie 的外部 Path 调整为 `/modelgw/api/user/auth`，写入和清除使用同一规则。会话提示 Cookie 限定在应用路径下，并同步验证读取和清除行为。保留 Secure、HttpOnly、SameSite 及原有认证保障，不通过扩大刷新 Cookie 作用域规避路径问题。

从旧根路径部署升级时需要重新登录。不增加旧 Cookie 清理、迁移或双路径读取逻辑。修改认证相关代码前按仓库要求阅读适用的 OWASP 指引，记录控制项与回归结果。

## 前端构建与运行

前端构建配置和运行代码复用一个不依赖浏览器环境的前缀定义；后端维护对应常量。两种语言各有声明，不宣称天然共享单一来源，也不为此增加代码生成流水线。

覆盖以下入口：

- Rsbuild 生产资源前缀、开发服务基础路径和 public 资源，包括 Logo、favicon。
- TanStack Router 的 basepath；路由库管理的导航不手工重复拼前缀。
- Axios 普通客户端和刷新客户端的 baseURL。
- 原生 fetch、流式下载、SSE/WebSocket，以及浏览器 location 跳转。
- OAuth 回调和复制给用户的服务地址。不新增旧链接转换或根路径兼容。

在已有路径工具可用时复用；确有缺口才增加小型应用 URL 工具。业务路径、外部绝对 URL、路由库路径必须区分，避免重复前缀和改写外部链接。

开发代理只匹配实际后端子路径，不代理整个 `/modelgw`；HTML、开发模块、静态资源和 HMR 由 Rsbuild 提供。生产构建和开发运行分别验证，不由其中一种推断另一种正常。

## 实施顺序

1. 固定当前 Go 基线，列出直接相关的 URL、Cookie、回调和部署配置使用方。
2. 实现入口挂载与路径边界测试，内部路由不动。
3. 实现前端路径、公开地址和 Cookie 适配，补充直接受影响流程的回归测试。
4. 验证开发模式及生产嵌入式前端，并更新部署所需公开地址和外部回调配置。
5. 验收通过后交付；不顺带改造数据库、计费、认证模型或后端语言。

## 验收矩阵

| 范围 | 验证方式与通过条件 |
| --- | --- |
| 入口 | 组装真实路由的 HTTP 集成测试：根路径 404、裸前缀 308、查询及编码路径正确、内部跳转保留前缀 |
| 控制台 | 内置浏览器验证生产静态资源、深链接刷新、导航、默认图标及失效登录跳转 |
| 登录 | 浏览器验证登录后刷新页面、刷新令牌、退出及重新登录；验证 Cookie Path 与清除行为 |
| OAuth | 本地可控提供方验证授权回调与 token 交换地址一致；真实提供方配置属于部署验收 |
| 协议 | 可控上游集成测试验证普通请求、SSE 及时刷新及断连、WebSocket 升级和双向通信 |
| MCP | 可控上游验证展示 URL、代理请求及协议返回的后续连接地址，必要时做协议级适配 |
| 公开链接 | 在既有合适测试中覆盖邮件、支付、媒体链接构造及独立公开地址优先级 |
| 开发环境 | 确认页面及 HMR 来自 Rsbuild，API 来自 Go，无前缀遗漏或重复 |

前端执行相关测试、类型检查、涉及文件 lint 和生产构建；后端执行受影响的聚焦测试。以完整用户流程为证据，不用纯 mock 转发测试代替部署验收。不发送真实收费上游请求，不在未获授权时修改第三方回调配置。

## 发布与回滚

发布前记录旧镜像及旧公开地址、回调和健康检查配置。健康检查、SDK Base URL、Webhook、OAuth 回调、监控探针均切换到带前缀地址。既有客户端必须调整 Base URL，此版本不承诺原根路径兼容。

回滚需同时恢复应用版本与相关地址配置，不能只回滚代码。预计无需数据库结构迁移；若实施中出现相关改动，另行评估并满足仓库三数据库验证要求。

## 非目标与完成条件

不迁移 Python，不重命名或删除 New API 与 QuantumNous 的项目标识，不扩展为通用多租户路径系统，不承诺零上游合并冲突。

仅当以上实际使用的入口和直接影响流程全部通过，才可宣布子路径部署完成。未能执行的外部提供方或部署验证须列为未完成项，不能以编译通过代替。

## 实施验证记录（2026-11-14）

- 后端通过：`go test ./router ./controller -run 'TestGatewayMount|TestGatewayCookiesAreScopedAndClearedAtExternalPaths|TestAuthLogoutRejectsRefreshCookieSessionMismatch' -count=1`，以及 `go test ./router -run TestGateway -count=1`。覆盖路径门禁、Gin 自动跳转、Cookie 写入与清除、真实连接上的 SSE 刷新和 WebSocket 双向通信。
- 前端通过：`bun run test -- src/lib/__tests__/app-path.test.ts src/lib/__tests__/server-error-notifications.test.ts src/lib/auth-session.test.ts src/features/usage-logs/components/__tests__/export-dialog.test.tsx src/features/keys/components/dialogs/__tests__/cc-switch-dialog.test.tsx src/features/dashboard/components/overview/__tests__/setup-guide.test.tsx src/features/auth/lib/auth-redirect.test.ts src/features/auth/lib/__tests__/oauth-popup.test.ts src/features/auth/lib/__tests__/oauth-callback-mode.test.ts src/features/auth/lib/oauth-bind-window.test.ts`，10 个文件、83 个测试通过。
- `bun run build` 通过；`CGO_ENABLED=0 GOWORK=off go build -ldflags='-s -w' -o /tmp/modelgw .` 通过，使用已有 `golang:1.26.10-alpine` 镜像。
- 独立生产构建服务 `http://localhost:5186/modelgw/` 上运行 `python .local-tests/modelgw/http_verify.py`，45 项 HTTP 检查通过：静态资源与深链接、登录与刷新、退出、令牌增删、模型列表、可控本地上游的普通 Chat 与 SSE、MCP 显式授权、目录 URL 与代理路径。没有发送真实付费请求。
- 开发模式 `VITE_REACT_APP_SERVER_URL=http://localhost:5186 bun run dev --port 5187`：带 `Accept: text/html` 的深链接请求返回开发 HTML；资源来自 Rsbuild，`/modelgw/api/status` 正确代理到 Go。HMR 的浏览器交互验证待完成。
- 类型检查未通过：本机 `bun run typecheck` 使用的 `bun check -b` 不可用；直接 `bun x tsc -b --pretty false` 报告既有的 API Key、日志 fixture 等类型问题。本次新增模块的类型错误已修复，未顺带修改其他功能。
- 涉及文件的 oxlint 检查仍有既有问题：`model-details-api.tsx` 中 5 处 replace/replaceAll 和 1 处 curly 错误；未扩大范围修复。用户原有 `CLAUDE.md` 改动另有末尾空行的 diff-check 提示，保持原样。
- 独立站点 `http://localhost:5186` 的浏览器验证此前被来源权限阻止。用户随后解除 `http://localhost:5175` 的权限并明确要求验证现有 Docker 站点，已完成首轮真实页面操作：模型搜索 BAAI 返回 3 项、供应商 OpenAI 筛选返回 5 项、筛选面板打开/关闭、模型详情及 API 标签、后台侧栏导航、API 密钥创建表单打开/取消、渠道卡片切换表格。
- 现有 Docker 站点发现未通过项：渠道页刷新后跳回 `/modelgw/sign-in?redirect=%2Fchannels`，同期容器日志中的刷新请求返回 401；Logo 使用 `/logo.png` 且加载失败；API 地址及模型调用示例仍使用 `http://localhost:3000`，缺少实际端口及 `/modelgw`。没有将这些项目计为通过。初期部分点击未改变页面，后续同类控件可用，未将其直接判定为代码缺陷。
- 当前 Docker 页面加载 `index.f3ac861938.js`，与已验证的新构建 `index.a7b006bd97.js` 不同；运行程序 SHA256 也不同。只能据此确认部署产物不一致，不能仅凭哈希推断具体缺失的修订。尚未替换或重启现有容器，需先部署一致构建，再验证刷新会话和受影响页面。现有 `ServerAddress` 需要管理员明确更新，不自动迁移旧配置。
- 浏览器验证使用现有登录会话及用户授权的本地登录；未提交密钥、渠道、余额等业务数据的新增、修改或删除，未调用收费上游。登录本身可能写入会话和认证审计记录，因此不将本轮现场验证描述为数据库零写入。截图保存在忽略目录 `.local-tests/modelgw/docker-api-example.jpg`，不含实际密钥或登录凭证。
- 管理页面其余按钮、登录刷新闭环、HMR、本地可控 OAuth、真实 OAuth 提供方、支付配置、邮件与媒体公开链接的部署验证仍未完成，不以本轮首轮页面操作代表全量验收。
- 用户授权后，已将 `new-api:modelgw-verified` 标记为 `new-api:local`，执行 `docker compose up -d --no-deps --no-build new-api` 更新 5175 应用容器；原镜像保留为 `new-api:modelgw-before-20261114`。PostgreSQL、Redis 启动时间未改变，应用保留原挂载，没有执行数据库清空或重建命令。
- 更新后浏览器确认加载 `index.a7b006bd97.js`，`/modelgw/logo.png` 正常显示；首页“开始使用”进入注册页，注册页“登录”进入登录页，随后已观察到后台概览。登录后的刷新回归已发起但被用户中断，尚未观察到结果，不计为通过。旧 `ServerAddress` 的现场配置仍待处理。
- 用户要求先提交当前实现；本次提交是阶段性保存，不表示全部浏览器及端到端验收已完成。

认证路径改动参考 [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [OAuth2 Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/OAuth2_Cheat_Sheet.html)。核对 ASVS 5.0.0 的 SameSite（3.3.2）、HttpOnly（3.3.4）与会话终止（7.4.1）相关控制：保留属性和服务端验证，只收窄 Cookie Path；测试验证写入/清除路径一致及退出后刷新失败。本次不是完整 ASVS 审计，未宣称认证体系整体合规；既有 Cookie 命名未使用 ASVS 3.3.1/3.3.3 建议的安全前缀，且本地 HTTP 测试不等同于生产 TLS 验收。

测试状态全部隔离：`modelgw-test` 只挂载 `.local-tests/modelgw/`，使用其中 `gateway.db`；没有连接或替换现有 `new-api`、`new-api-pg` 与其数据卷。临时目录保存测试二进制、SQLite 和验证日志，由当前源代码重建；保留到浏览器验收结束，之后可停止并删除测试容器和目录。`modelgw-build` 保存本次构建缓存，验收结束后可删除。生成物均被已有 `.gitignore` 忽略，不提交 Git。
