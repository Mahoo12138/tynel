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
	"strconv"
)

// tynelNumberFromBits 按位构造一个 number。go/constant 表示不了 NaN、
// 无穷和负零，bridge 对这三类值改用它，避免位模式在编译期被改写。
func tynelNumberFromBits(bits uint64) float64 {
	return math.Float64frombits(bits)
}

// tynelFormatNumber 把 binary64 写成一行文本。
//
// 有限值用最短且可回读的十进制写法（strconv 的 'g' 格式、精度 -1），所以
// 0.1 + 0.2 输出 0.30000000000000004，而不是四舍五入后的 0.3。四个特殊值
// 用固定拼写，避免不同格式化路径给出不同结果。
//
// 这套拼写是本阶段自定的，不是 JavaScript console.log 的协议：那边无穷写作
// Infinity，负零也写作 -0，而这里的 +Inf / -Inf 沿用 Go 的写法。接上完整的
// 数值语义时（03.x）会一并改。
func tynelFormatNumber(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case v == 0 && math.Signbit(v):
		return "-0"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// tynelPrintFloat64 是 NIR 里 print 语句的目标。
//
// 每次调用写一行，行尾一个换行符。它是受限输出，不是 Node 的 console：
// 只接受一个 number，没有格式化占位符，也不写标准错误。
func tynelPrintFloat64(v float64) {
	fmt.Fprintln(os.Stdout, tynelFormatNumber(v))
}

// tynelPrintBool 输出 boolean。拼写固定为 true 与 false，与 Go 的默认格式
// 一致，但不依赖它——输出的拼写是编译器约定的一部分。
func tynelPrintBool(v bool) {
	if v {
		fmt.Fprintln(os.Stdout, "true")
		return
	}
	fmt.Fprintln(os.Stdout, "false")
}
