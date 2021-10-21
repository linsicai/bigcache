package bigcache

import "time"

// 时钟接口
type clock interface {
	Epoch() int64
}

// 系统时钟
type systemClock struct {
}

// 当前时间
func (c systemClock) Epoch() int64 {
	return time.Now().Unix()
}
