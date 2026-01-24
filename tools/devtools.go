//go:build tools
// +build tools

// Package tools 用于管理开发工具依赖
// 这些工具会被锁定在 go.mod 中，确保团队成员使用相同版本
//
// 安装开发工具：
//
//	go install github.com/go-delve/delve/cmd/dlv@latest
//	go install golang.org/x/tools/gopls@latest
//
// 或者使用 go generate 一键安装：
//
//	go generate ./tools/devtools.go
package tools

import (
	// delve debugger - VSCode Go 扩展使用
	_ "github.com/go-delve/delve/cmd/dlv"

	// gopls - Go 语言服务器（代码补全、跳转等）
	_ "golang.org/x/tools/gopls"

	// staticcheck - 静态分析工具
	_ "honnef.co/go/tools/cmd/staticcheck"
)

//go:generate go install github.com/go-delve/delve/cmd/dlv
//go:generate go install golang.org/x/tools/gopls
//go:generate go install honnef.co/go/tools/cmd/staticcheck
