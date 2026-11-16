package vector

// SetEmbedBytes 供测试调小一次交给模型的媒体内容的上限，返回恢复函数。
func SetEmbedBytes(n int) func() {
	old := embedBytes
	embedBytes = n
	return func() { embedBytes = old }
}
