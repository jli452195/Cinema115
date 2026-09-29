package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"strm115/backend/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	defaultDir := os.Getenv("DATA_DIR")
	if defaultDir == "" {
		defaultDir = "../data"
	}
	directory := flag.String("data-dir", defaultDir, "数据目录")
	action := flag.String("action", "status", "操作：status、migrate、backup、reset-portal（停服后清空个人中心测试数据）")
	output := flag.String("output", "", "备份文件路径，必须尚不存在")
	flag.Parse()
	if *action != "status" && *action != "migrate" && *action != "backup" && *action != "reset-portal" {
		return fmt.Errorf("操作应为 status、migrate、backup 或 reset-portal")
	}
	if (*action == "backup" || *action == "reset-portal") && *output == "" {
		return fmt.Errorf("备份必须指定 -output")
	}
	path := filepath.Join(*directory, "cinema115.db")
	// 查询和备份不能因路径写错而创建空数据库。
	if *action != "migrate" {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("数据库不存在或不可访问: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := storage.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if *action == "reset-portal" {
		if err := db.Backup(ctx, *output); err != nil {
			return err
		}
		if err := db.ResetPortal(ctx); err != nil {
			return err
		}
		fmt.Println("已备份并重置个人中心测试数据，管理员网盘与媒体资料保留；原 Emby 账号未删除")
		return nil
	}
	if *action == "migrate" {
		result, err := db.ImportLegacyJobs(ctx, filepath.Join(*directory, "reshare-jobs"))
		if err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			return err
		}
	}
	if err := db.Check(ctx); err != nil {
		return err
	}
	if *action == "backup" {
		if err := db.Backup(ctx, *output); err != nil {
			return err
		}
		fmt.Println("数据库备份完成")
		return nil
	}
	status, err := db.Status(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(status)
}
