package bigcache

import (
	"encoding/binary"
)

const (
    // 时钟大小，8字节
	timestampSizeInBytes = 8                                                       // Number of bytes used for timestamp
	// hash 大小，8字节
	hashSizeInBytes      = 8                                                       // Number of bytes used for hash
	// key大小，2字节
	keySizeInBytes       = 2                                                       // Number of bytes used for size of entry key
	// 头部大小18
	headersSizeInBytes   = timestampSizeInBytes + hashSizeInBytes + keySizeInBytes // Number of bytes used for all headers
)

func wrapEntry(timestamp uint64, hash uint64, key string, entry []byte, buffer *[]byte) []byte {
    // 计算key 长度
	keyLength := len(key)
	// 计算blob 长度，头部+key大小+实体大小
	blobLength := len(entry) + headersSizeInBytes + keyLength

    // 调整缓冲区
	if blobLength > len(*buffer) {
		*buffer = make([]byte, blobLength)
	}
	blob := *buffer

    // 编码时间
	binary.LittleEndian.PutUint64(blob, timestamp)
	
	// 编码hash 值
	binary.LittleEndian.PutUint64(blob[timestampSizeInBytes:], hash)
	
	// 编码key 长度
	binary.LittleEndian.PutUint16(blob[timestampSizeInBytes+hashSizeInBytes:], uint16(keyLength))

	// 复制值和实体
	copy(blob[headersSizeInBytes:], key)
	copy(blob[headersSizeInBytes+keyLength:], entry)

	return blob[:blobLength]
}

// 实体append
func appendToWrappedEntry(timestamp uint64, wrappedEntry []byte, entry []byte, buffer *[]byte) []byte {
    // reset 缓冲区大小
	blobLength := len(wrappedEntry) + len(entry)
	if blobLength > len(*buffer) {
		*buffer = make([]byte, blobLength)
	}
	blob := *buffer

	// 更新时间
	binary.LittleEndian.PutUint64(blob, timestamp)
	// 老值
	copy(blob[timestampSizeInBytes:], wrappedEntry[timestampSizeInBytes:])
	// 追加的值
	copy(blob[len(wrappedEntry):], entry)

	return blob[:blobLength]
}

func readEntry(data []byte) []byte {
    // 获取长度
	length := binary.LittleEndian.Uint16(data[timestampSizeInBytes+hashSizeInBytes:])

	// copy on read
	// 复制值
	dst := make([]byte, len(data)-int(headersSizeInBytes+length))
	copy(dst, data[headersSizeInBytes+length:])

    // 返回字节流
	return dst
}

// 解析时间
func readTimestampFromEntry(data []byte) uint64 {
	return binary.LittleEndian.Uint64(data)
}

// 解析key
func readKeyFromEntry(data []byte) string {
    // 获取长度
	length := binary.LittleEndian.Uint16(data[timestampSizeInBytes+hashSizeInBytes:])

	// copy on read
	// 复制key
	dst := make([]byte, length)
	copy(dst, data[headersSizeInBytes:headersSizeInBytes+length])

    // 转字符串
	return bytesToString(dst)
}

// key 比较，不需要复制出来
func compareKeyFromEntry(data []byte, key string) bool {
	length := binary.LittleEndian.Uint16(data[timestampSizeInBytes+hashSizeInBytes:])

	return bytesToString(data[headersSizeInBytes:headersSizeInBytes+length]) == key
}

// 获取hash 值
func readHashFromEntry(data []byte) uint64 {
	return binary.LittleEndian.Uint64(data[timestampSizeInBytes:])
}

// 重置
func resetKeyFromEntry(data []byte) {
	binary.LittleEndian.PutUint64(data[timestampSizeInBytes:], 0)
}
