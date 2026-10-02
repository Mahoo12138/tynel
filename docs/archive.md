# 旧原型归档与恢复

## 保存的内容

旧 `main` 重命名为 `archive`，保留完整的五阶段提交链。归档顶端仍是 `bd1da61876be7acdbee2f57bc2a1fdb0c76c7295`，没有为重新开始而修改旧实现。

旧阶段标签在本地归入以下命名空间。引用仍指向原来的标签对象；旧标签对象中的原始名称和说明保持不变。

| 原标签 | 归档标签 |
| --- | --- |
| `stage/01-ir-native` | `archive/stage/01-ir-native` |
| `stage/02-tsgo-query` | `archive/stage/02-tsgo-query` |
| `stage/03-ts-native` | `archive/stage/03-ts-native` |
| `stage/04-control-flow` | `archive/stage/04-control-flow` |
| `stage/05-short-circuit` | `archive/stage/05-short-circuit` |

上一轮第一篇候选改稿保存在 `archive/worktrees/note01-clarify`，对应 `f758a7e49b9569a42bee4f32aec5f0d24f0cb72f`。其余四个历史 worktree 的提交也使用 `archive/worktrees/*` 引用固定，具体名称和身份见[归档清单](archive-manifest.json)。这组引用用于备份，不代表新增学习阶段。

原有 worktree、`.tynel/`、`.upstream/`、`.research/` 和 `dist/` 均保留在磁盘上。它们不进入新 MVP 的构建路径。分支和 Git bundle 保存的是版本控制对象，不包含忽略目录里的 SDK、缓存或二进制；这些目录未被删除，但也没有声称已作完整的离机备份。

## 独立 Git 备份

备份文件位于：

```text
D:\Code\Tynel\.tynel\restart-20261015\pre-restart.bundle
```

bundle 大小为 331,068 字节，SHA-256 为：

```text
55f9af50212cf993a5d9f90c05d9fbf789e97eda6c6934fe7da3c1446e443529
```

本次已执行 `git bundle verify`，结果确认备份包含完整历史。bundle 保留归档分支、原始阶段标签、归档标签和固定的历史 worktree 提交。它存放在本机项目目录中，不是异地灾备。

## 查看归档而不切换新开发目录

目标目录必须尚不存在。在 `D:\Code\Tynel` 中执行：

```powershell
# 单独检出旧项目，不改变当前 main 的文件。
git worktree add --detach .work/archive-study archive
if ($LASTEXITCODE -ne 0) { throw '归档检出失败，请检查目标目录。' }
```

查看某个旧节点时，使用表中的归档标签。旧文档内部仍记录原来的 `stage/*` 名称；需要按原名检查整套旧标签时，应从 bundle 建立独立副本，不要把旧标签重新混入新开发仓库。

```powershell
# 在新的目录恢复旧仓库和原始标签；只读取本地 bundle。
# .work/archive-copy 必须尚不存在。
git clone -b archive .tynel/restart-20261015/pre-restart.bundle .work/archive-copy
if ($LASTEXITCODE -ne 0) { throw '归档恢复失败，请保留备份并检查错误。' }
```

恢复后仍需按旧锁文件准备 SDK，Git 备份不包含那些忽略的构建依赖。

## 与远程的关系

本次只整理本地。`origin` 配置和 `origin/main` 跟踪引用保持原样，未推送 `archive` 或新 `main`，未删除远程标签。新 `main` 从独立根提交开始，不应直接拉取旧远程历史来合并。

后续远程迁移需要明确发布归档、更新默认分支及标签的操作范围，不能把本地整理成功当作远程已经同步。
