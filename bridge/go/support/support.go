//go:build tynelapp

// Package main 是 Tynel 的固定支持文件。
//
// 它只提供 NIR 无法表达、也不该由用户书写的最小宿主支持：把 number 输出到
// 标准输出。用户函数不在这里，它们由 bridge 从 NIR 构造成 IR 节点后加入同一
// 个包。这个文件对所有输入都相同，不随 NIR 变化。
package main

import (
	"fmt"
	"math"
	"os"
)

// tynelNumberFromBits 按位构造一个 number。go/constant 表示不了 NaN、
// 无穷和负零，bridge 对这三类值改用它，避免位模式在编译期被改写。
func tynelNumberFromBits(bits uint64) float64 {
	return math.Float64frombits(bits)
}

// tynelPrintFloat64 是 NIR 里 print 语句的目标。
// binary64 按 Go 的默认格式输出：42 打印成 42，0.5 打印成 0.5。
func tynelPrintFloat64(v float64) {
	fmt.Fprintln(os.Stdout, v)
}
