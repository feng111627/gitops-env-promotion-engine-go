# gitops-env-promotion-engine-go

本地化的环境配置渲染与晋级引擎：用本地文件模拟基础配置、各环境覆盖层与已应用状态，
保证不同环境下的最终配置、晋级与回滚结论一致且可解释。

## 目录布局

```
examples/
  base.json          # 基础配置层（优先级最低）
  envs/<env>.json    # 环境覆盖层（dev / prod / ...）
  schema.json        # 字段默认语义与约束（必填 / 默认值 / 取值范围）
  policy.json        # 声明式策略（必填 / 范围 / 禁止明文密钥）
  secrets.json       # 外部注入的密钥源（本地模拟）
state/
  <env>/current.json         # 当前已应用版本
  <env>/history/<seq>.json   # 全部历史版本
```

## 分层与合并规则

- 合并顺序固定：`base.json` → `envs/<env>.json`，**后面的层覆盖前面的层**；
  同名嵌套 map 递归合并，其余值整体替换。
- **类型冲突不静默处理**：同名字段在不同层类型不一致（如 string vs map）时，
  记录 `type_conflict` 拒绝并保留原值，绝不静默覆盖或丢弃。
- **拒绝原因可区分**：`type_conflict`（类型不一致）、`missing_required`（必填缺失）、
  `out_of_range`（取值越界）、`invalid_layer`（层文件无法解析）。
- **兼容旧配置**：旧配置缺少后来新增的字段时，按 `schema.json` 中的 `Default`
  正常渲染；无默认值且必填才报 `missing_required`。

## 晋级、回滚与漂移语义

- **晋级（promote）**：渲染 → 策略校验 → 密钥解析全部通过后，作为新版本写入
  `history/<seq>.json` 并更新 `current.json`（历史先落盘，当前指针后更新）。
  内容与当前版本一致时返回 `ErrNoChange`，**不产生重复版本、不污染历史**。
- **回滚（rollback）**：精确切回 Seq 次新的历史版本；没有更早版本时返回
  `ErrNoPrevious` 明确拒绝。
- **漂移检测（drift）**：递归比较「期望渲染结果」与「当前已应用版本」，
  逐字段列出 `期望 vs 实际`；从未应用过的环境整体视为漂移。
- **并发安全**：每个环境一把互斥锁，多个晋级请求串行写入，
  版本号连续递增，不会交错写坏状态。

## 策略与密钥

- **策略**：`policy.json` 声明规则（`Required` / `Min` / `Max` /
  `NoPlaintextSecret`）。校验一次性汇总**所有**违规（按环境、字段、规则），
  不遇到第一条就中断；存在违规即拒绝晋级。
- **密钥**：配置中的敏感字段写 `secret://name` 引用，由外部注入的密钥源
  （`secrets.json` 模拟）在渲染后解析。缺失的密钥按字段逐个报错，
  原引用保留不动，**不以空值或占位值蒙混**。

## 本地验证

```bash
# 运行全部单测（覆盖合并优先级、字段冲突、兼容旧配置、晋级回滚与漂移、
# 并发晋级、策略违规、密钥缺失；日志标注每个用例覆盖的业务）
go test ./... -v

# 含竞态检测
go test -race ./...

# 渲染某环境的最终配置（含默认值补全与密钥解析）
go run ./cmd/engine render prod

# 晋级 / 重复晋级（幂等）/ 漂移 / 回滚
go run ./cmd/engine promote prod
go run ./cmd/engine promote prod   # 提示配置一致，不产生新版本
go run ./cmd/engine drift prod
go run ./cmd/engine rollback prod
```

修改 `examples/envs/prod.json` 后再次 `promote` 可看到版本递增；
`drift` 会逐字段列出期望与已应用的差异。
