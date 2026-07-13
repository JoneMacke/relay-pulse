// Package storage 提供数据存储相关的公共工具函数
package storage

// b2i 把 bool 转成 SQLite 用的 0/1 整数（SQLite 无原生 bool 类型）。
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// reverseRecords 反转记录数组（DESC 取数后翻转为时间升序）
func reverseRecords(records []*ProbeRecord) {
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
}
