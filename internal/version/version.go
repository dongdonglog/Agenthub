// Package version 提供构建版本信息。
package version

// Version 是当前构建版本，发布流程通过 -ldflags 覆盖。
var Version = "0.1.0-dev"

// Commit 是构建对应的提交号，可为空。
var Commit = ""
