# Passkey 基线 fixture

`baseline-credential.json` 是 **B0（`4697f29b`）在改动 Passkey 存储之前**导出的测试凭证数据。它存在的唯一理由：H3 和 M2 必须能对**同一份**凭证数据证明"不重新注册即可登录"，而任何在 H3 之后生成的 fixture 都会被迁移后的存储形状污染，无法再作为基线。

## 生成方式

`webauthn.Credential` 用真实结构体构造后 `json.Marshal` —— 与 `internal/service/passkey/passkey.go` 存库走的路径同一形状（`json.Marshal(cred)`），不是手写的 JSON。测试专用：公钥不是秘密，但**不要**把任何真实 IdP/用户数据加进这个文件。

## 它固化的不变式（ADR 0036 §/计划书 §3.2）

| 不变式 | 本文档中的字段 |
| --- | --- |
| user handle 为 `BigEndian.PutUint64(uint64(userID))` 的固定 8 字节 | `user_id` / `handle_hex` |
| 凭证 ID 在库里是 base64url 无填充 | `credential_id` / `credential_id_encoding` |
| 凭证记录是完整 `webauthn.Credential` 的 JSON | `credential` |
| 计数器与克隆判定所需的 sign count 与 flags | `credential.authenticator.signCount`、`credential.flags` |

**一个容易踩的编码不对称**：`credential.id` 是**标准 base64 带填充**（Go 对 `[]byte` 的默认 JSON 编码），而 `credential_id` 列是 **base64url 无填充**。两者是同一个值的不同编码，适配层必须显式做 `raw ID → Raw URL Base64` 转换，不能假定它们字符串相等。

## 使用约定

- **H3 必须先加消费它的测试**（W01：用这份数据完成一次真实 WebAuthn ceremony，断言 handle / RP ID / Origin 与基线一致），再改动存储实现。
- **M2 复用同一份文件**，不得重新生成；若迁移后需要更新 fixture，那本身就是一个需要记录的行为变化。
- 本目录不存放私钥。真实 IdP 样本、SP 私钥与密钥材料一律不入库（见 `docs/authcore-deployment-compatibility.md` §3）。
