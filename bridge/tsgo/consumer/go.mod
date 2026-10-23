module github.com/Mahoo12138/tynel/bridge/tsgo/consumer

go 1.27

require github.com/microsoft/typescript-go v0.0.0

require (
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

// 上游模块的包全是 internal，只能由模块树内的代码导入。适配包被复制进
// .work/frontend/typescript-go/tyneladapter，本模块通过 replace 指向那棵树，
// 因此仍然是一个普通的外部模块：不复制 internal 包，也不绕过访问规则。
replace github.com/microsoft/typescript-go => ../../../.work/frontend/typescript-go
