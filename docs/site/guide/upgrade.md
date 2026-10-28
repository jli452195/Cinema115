---
title: 更新与备份
sidebarSort: 40
---

# 更新与备份

更新前保留当前 `compose.yaml`、原 `JWT_SECRET` 和运行数据。镜像可以重新下载，配置与任务数据需要自己备份。

## 备份数据库

服务运行时，使用容器自带工具创建一致性快照：

```sh
docker compose exec cinema115 /app/dbtool -action backup -output /data/backups/cinema115-before-upgrade.db
```

目标文件必须不存在；每次备份请使用不同文件名。上述文件会出现在 NAS 挂载的 `data/backups/` 中。

::: warning 完整备份
数据库备份不等于完整部署备份。完整迁移或恢复还需保留 Compose 配置、原 `JWT_SECRET` 与运行目录中的其他数据。

需要复制整个 `data/` 目录时，先执行 `docker compose stop`，再通过 NAS 文件管理器或备份工具复制；不要只复制运行中的 `cinema115.db`。
:::

## 更新镜像

查看 [GitHub Releases](https://github.com/jli452195/Cinema115/releases) 的更新说明。使用固定版本时，修改自己 Compose 文件中的 `image`，例如：

```yaml
image: jli452195/cinema115:版本号
```

把 `版本号` 替换成 Releases 中目标版本对应的镜像标签，例如 Git 标签 `v0.1.2` 对应镜像标签 `0.1.2`。使用 `latest` 则跟随正式版更新。

保留自己已填写的环境变量与挂载路径，执行：

```sh
docker compose pull
docker compose up -d
docker compose ps
```

## 核对版本与状态

```sh
docker compose exec cinema115 /app/server -version
docker compose exec cinema115 /app/dbtool -action status
docker compose logs --tail=100 cinema115
```

确认容器正常，再打开网页核对账号连接和任务状态。

## 恢复注意事项

旧镜像不保证能读取升级后的数据库。需要回退时，先停止服务，使用升级前备份恢复相匹配的配置和数据，再运行对应版本。

恢复数据库快照前，备份当前数据库及其 WAL/SHM 文件，避免恢复后的数据库混用旧事务文件。恢复完成后检查任务状态和云端操作结果，再继续自动任务。
