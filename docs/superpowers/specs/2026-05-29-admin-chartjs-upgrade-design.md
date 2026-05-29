# Admin Chart.js 图表升级设计

## 背景

管理后台的概览页和用量页已有 token 曲线图，但当前实现是手写 SVG，视觉表现和交互都较弱。目标是在不改变后端 API 和 admin UI 静态嵌入形态的前提下，引入更美观、可维护的轻量绘图组件。

## 决策

使用 Chart.js 作为本地 vendored 图表库，由 Go embed 随 `internal/adminui` 静态资源一起服务。`index.html` 在 `app.js` 之前加载本地 `chart.umd.min.js`。不使用 CDN，确保内网和 Docker 部署不依赖外部网络。

本次只替换两处曲线图：

- 概览页：最近 30 天 Total Token 趋势。
- 用量页：员工 Total/Input/Output/Cache Token 趋势。

`Model 汇总` 保持现有表格，不新增柱状图或排行图。

## 架构

保留当前无构建、纯静态 admin UI：

- 新增本地 Chart.js UMD 文件到 `internal/adminui/` 下的 vendor 静态资源路径。
- 更新 `internal/adminui/static.go` 的 embed 清单，确保库文件随二进制发布。
- 更新 `internal/adminui/index.html`，先加载 Chart.js，再加载 `app.js`。
- 在 `internal/adminui/app.js` 新增小型 chart helper/registry。

chart registry 负责：

- 创建概览和用量图表。
- 在 `renderShell()` 替换页面内容前销毁旧 Chart 实例。
- 统一 Chart.js 默认样式、tooltip、坐标轴、数字格式和错误降级。

这样两个页面共享图表配置基础，避免 tooltip、颜色、销毁逻辑重复散落在页面函数里。

## 数据流

后端 API 不变。

概览页继续读取：

- `/admin/api/overview`
- `overview.token_usage_daily[]`
- 字段：`date`, `total_tokens`

用量页继续读取：

- `/admin/api/usage?username=...&range=...&model=...&bucket_size=day`
- `employee_usage.daily[]`
- 字段：`bucket_start` 或 `date`, `total_tokens`, `prompt_tokens`, `completion_tokens`, `cached_tokens`

前端渲染流程：

1. `renderOverview()` 或 `renderUsage()` 输出 `<canvas>` 容器和空态容器。
2. DOM 写入完成后调用对应 chart initializer。
3. initializer 将 API 数据适配为 Chart.js `labels` 和 `datasets`。
4. 筛选切换仍走现有 `reloadUsageView()`，重新渲染 DOM，再创建新图表。

空数组或全 0 数据显示中文空态，不初始化 Chart.js，避免出现贴底的无意义曲线。

## 视觉与交互

整体风格保持后台监控感：克制、清晰、适合扫描，不做营销式大面积装饰。

概览图：

- 单条 Total Token 曲线。
- 蓝色主线，浅蓝面积填充。
- 浅色网格，Y 轴使用 compact number。
- tooltip 显示日期和格式化 Total Token。

用量图：

- 四条曲线：Total、Input、Output、Cache。
- 颜色沿用现有语义：Total 蓝、Input 绿、Output 橙、Cache 紫。
- 保留现有图例区域，但用新的样式与 Chart.js 数据一致。
- tooltip 同时展示同一天的多条 token 值。

移动端：

- 图表保持固定的响应式高度。
- canvas 宽度跟随容器。
- 现有搜索框、range tabs、model tabs、表格布局不改。

## 错误处理与降级

- 如果 Chart.js 未加载，图表区域显示“图表组件加载失败”，其他指标、筛选和表格继续可用。
- 如果图表初始化抛错，只影响对应图表区域，不中断整个 admin shell。
- API 错误沿用现有错误面板。
- 每次页面重绘前销毁旧 Chart 实例，避免内存泄漏和重复事件监听。

## 非目标

- 不改后端 API、数据库 schema 或 usage 聚合逻辑。
- 不引入前端构建工具。
- 不把 `Model 汇总` 改成图表。
- 不重构整个 admin UI 组件体系。

## 验收标准

- 概览页和用量页不再使用手写 SVG 曲线图。
- Chart.js 从本地 admin 静态资源加载，不依赖 CDN。
- 概览页显示最近 30 天 Total Token 曲线和 hover tooltip。
- 用量页显示员工 Total/Input/Output/Cache 曲线，range/model 切换后图表正确刷新。
- 空数据时显示中文空态，不渲染无意义曲线。
- Chart.js 加载失败时页面不崩，其他后台功能仍可用。
- 浏览器控制台无图表初始化、筛选切换或重复销毁相关错误。

## 验证计划

- 运行 Go admin 相关测试，确认 embed 和 API 行为没有破坏。
- 启动 admin UI，使用浏览器检查：
  - `chart.umd.min.js` 由本地 `/admin/...` 路径加载。
  - 概览页 canvas 非空且 tooltip 可用。
  - 用量页输入员工后 canvas 非空。
  - 切换 `1d`、`7d`、`30d` 和 model filter 不产生 console error。
  - 空数据和 Chart.js 缺失降级文案可见。
