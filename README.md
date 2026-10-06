# GitOps 环境配置渲染与晋级引擎（本地版）

用本地文件模拟「基础配置 + 各环境覆盖层 + 已应用状态」，实现一套可解释、可复现的
环境配置分层渲染、晋级（promotion）、回滚（rollback）与漂移检测（drift detection）。
不依赖任何外部集群或数据库，所有状态都落在本地 JSON 文件中。

## 目录结构

```
configs/
  base.json                # 基础配置（所有环境共享）
  schema.json              # 新增字段的类型与确定默认值（旧配置兼容语义）
  policy.json              # 声明式策略（必填/范围/枚举/正则/禁止明文密钥）
  secrets.example.json     # 外部密钥来源示例（运行时用 --secrets 注入）
  overlays/
    dev.json               # 环境覆盖层
    staging.json
    prod.json
  state/state.json         # 运行后生成的已应用状态（含版本历史，勿提交）
internal/
  model/                   # 共享类型、拒绝原因码、版本记录
  render/                  # 分层合并 + 本地文件加载
  secret/                  # 外部密钥引用解析
  policy/                  # 声明式策略校验
  store/                   # 文件锁 + 原子写的状态存储
  engine/                  # 渲染/晋级/回滚/漂移编排
cmd/gitopsctl/             # 命令行入口
```

## 一、分层与合并规则

合并顺序（优先级自低到高，固定不变）：

1. **default** —— `schema.json` 声明的新增字段默认值；
2. **base** —— `configs/base.json`，全环境共享；
3. **overlay** —— `configs/overlays/<env>.json`，环境覆盖层。

合并语义：

- 对象按 key **深度合并**；
- 数组**整体替换**，不做下标级合并（简单、固定、可解释）；
- 标量同类型时 overlay 覆盖 base；
- 显式 `null` 表示「该层未设置」，由低优先级层生效，不会把字段清空；
- 渲染结果中的每个叶子字段都带 **来源层**（`default` / `base` / `overlay:<env>`），
  `gitopsctl render` 会逐字段打印，结论可解释。

### 拒绝原因（不会被静默覆盖或丢弃）

所有问题都带 `code / env / field / rule / message`，一次性汇总返回：

| 场景 | 拒绝码 |
| --- | --- |
| base 与 overlay 同名字段类型不一致 | `TYPE_MISMATCH`（overlay 被拒绝，保留 base 值） |
| schema 声明类型与实际值不符 | `TYPE_MISMATCH` |
| 必填字段缺失或为空 | `MISSING_REQUIRED` |
| 数值超出 `[min, max]` | `OUT_OF_RANGE` |
| 字符串不在枚举内 | `ENUM_VIOLATION` |
| 字符串不匹配正则 | `PATTERN_VIOLATION` |

### 旧配置兼容（新增字段不报错）

后来新增的字段写在 `schema.json` 中并给出**确定默认值**。旧配置缺少这些字段时，
合并结果会自动补齐默认值并把来源标记为 `default`，不会因为缺字段而失败。
默认值支持嵌套路径（如 `app.tracing.enabled`），父对象不存在时会自动建立。

## 二、晋级、回滚与漂移

已应用状态保存在 `configs/state/state.json`，每个环境维护一条**只追加**的版本历史。
版本记录包含：版本号（v1、v2…）、父版本、动作（apply/promote/rollback）、
来源环境与来源版本、时间戳、规范化 checksum（SHA-256，键排序，与环境无关）、
以及**最终配置快照**（已经过密钥注入与策略校验的值）。

### apply（首次/重新应用）

`apply <env>` 渲染当前声明式配置：合并 → 密钥注入 → 策略校验，任一阶段有拒绝
原因则**整体拒绝，不产生任何版本**；全部通过才追加新版本。

### promote（晋级）

`promote <src> <dst>` 复制**源环境当前已应用版本的快照**到目标环境（而不是重新
渲染目标 overlay），因此不同环境的最终配置严格一致，checksum 相同。

- **重复晋级拒绝**：目标最新版本已来自「源环境 + 源版本」且 checksum 相同，
  返回 `DUPLICATE_PROMOTION`，不产生新版本、不污染历史；
- 源环境产生新版本后可再次晋级，目标得到新版本；
- **并发安全**：整个读-改-写由进程内互斥锁 + `state.lock` 上的 `flock(LOCK_EX)`
  串行化，落盘用「临时文件 + rename」原子替换。多个相同晋级请求同时发起时，
  恰好 1 个成功，其余以 `DUPLICATE_PROMOTION` 明确拒绝；源环境从未应用时返回
  `NOT_FOUND`。

### rollback（回滚）

`rollback <env>` 精确回到**上一个已应用版本**：

- 追加一条 `rollback` 记录，其快照内容与目标版本 checksum 相同，并通过
  `parent` / `restoredFrom` 精确指向来源，历史只追加、可审计；
- 连续回滚沿「有效版本链」单调向前（回滚记录的上一版取其 `restoredFrom`
  版本的父版本），**不会在两个快照之间来回弹跳**；
- 已处于最初版本内容、或环境从未应用时，返回 `NOTHING_TO_ROLLBACK`，
  绝不凭空构造空配置版本。

### drift（漂移检测）

`drift <env>` 逐字段对比「已应用版本快照」与「当前声明式配置渲染结果」：

- 只比较已应用版本中记录过的字段；overlay/base 里全新增加的字段属于新版本，
  不算漂移，需要新的 apply/promote 才进入已应用状态；
- 值不同、或字段在声明式配置中被删除/置空都会逐字段报告 `expected/actual`；
- 当前渲染存在合并/密钥/策略违规时，违规随结果一并返回，不被静默吞掉；
- 重新 apply（或晋级到新版本）后漂移清除。

## 三、声明式策略与外部密钥

### 策略（configs/policy.json）

支持的规则 `kind`：`required`、`range`、`enum`、`pattern`、`noPlaintextSecret`。
规则可用 `envs` 限定生效环境（为空表示所有环境）。校验**遍历全部规则、汇总全部
违规**，绝不碰到第一条就中断；每条违规都带环境、字段与规则名。

### 密钥（外部注入）

- 配置中密钥字段必须写成引用：`secret://<name>`（例如 `secret://db/password`）；
- 真实值来自外部解析器，CLI 通过 `--secrets=<file>`（JSON map）或
  `GITOPS_SECRETS_FILE` 注入；
- `noPlaintextSecret` 策略保证密钥字段**禁止明文**；
- 外部来源缺少某个引用、或值为空字符串时，**按字段逐个**报 `SECRET_MISSING`，
  缺失位保留引用、绝不写入空值或占位值（如 `changeme`/`***`）；
- 只有全部密钥解析成功、且无策略违规时，才允许写入已应用版本。

## 四、本地验证

要求 Go 1.25+。

```bash
# 1) 渲染（可看到每个字段的来源层 default/base/overlay:<env> 与最终配置）
go run ./cmd/gitopsctl --configs=configs --secrets=configs/secrets.example.json render dev

# 2) 首次应用
go run ./cmd/gitopsctl --configs=configs --secrets=configs/secrets.example.json apply dev --message="initial"

# 3) 晋级到 staging，再重复一次（第二次应被 DUPLICATE_PROMOTION 拒绝）
go run ./cmd/gitopsctl --configs=configs --secrets=configs/secrets.example.json promote dev staging
go run ./cmd/gitopsctl --configs=configs --secrets=configs/secrets.example.json promote dev staging

# 4) 修改 configs/overlays/dev.json 后重新 apply，再检测漂移
go run ./cmd/gitopsctl --configs=configs --secrets=configs/secrets.example.json drift staging

# 5) 回滚到上一个已应用版本
go run ./cmd/gitopsctl --configs=configs --secrets=configs/secrets.example.json rollback staging
```

### 运行测试

```bash
go test ./... -race -v
```

测试覆盖的业务场景（每个用例都用中文日志打印业务说明与通过结论）：

- 合并优先级：base-only 保留、overlay 覆盖、对象深合并、数组整体替换、来源层；
- 字段冲突：类型不一致逐字段 `TYPE_MISMATCH`，base 值保留；显式 null 语义；
- 兼容旧配置：新增标量/嵌套对象/兄弟对象按 schema 默认值补齐，不失败；
- 晋级/回滚/漂移：跨环境 checksum 一致、重复晋级拒绝、历史只追加、
  回滚精确恢复且不再来回弹跳、漂移逐字段报告后重新应用清除；
- 并发晋级：25 个相同晋级请求恰好 1 成功、24 个被拒，历史只有一条（`-race`）；
- 并发存储：30 个并发写串行落盘、条目精确、文件是完整 JSON、拒绝不写盘；
- 策略违规：required/range/enum/pattern/明文密钥 5 类一次性汇总、按环境生效、
  边界值、`TYPE_MISMATCH` 区分；
- 密钥缺失：多字段逐个 `SECRET_MISSING`、空字符串视为缺失、入参不被改写。

> 运行时生成的 `configs/state/` 目录含已注入的配置快照（权限 0600），
> 属于本地运行产物，可随时删除后从头演练。
