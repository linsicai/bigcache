package bigcache

// 取大函数
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// 取小函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MiB 转字节数
func convertMBToBytes(value int) int {
	return value * 1024 * 1024
}

// 是否2 的倍数
// 2n 肯定满足
// （2n + x）&（2n + x - 1）肯定不满足
func isPowerOfTwo(number int) bool {
	return (number != 0) && (number&(number-1)) == 0
}
