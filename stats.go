package bigcache

// Stats stores cache statistics
// 统计接口
type Stats struct {
	// Hits is a number of successfully found keys
	// 命中缓存
	Hits int64 `json:"hits"`
	// Misses is a number of not found keys
	// 不命中缓存
	Misses int64 `json:"misses"`
	// DelHits is a number of successfully deleted keys
	// 删除命中
	DelHits int64 `json:"delete_hits"`
	// DelMisses is a number of not deleted keys
	// 删除不命中
	DelMisses int64 `json:"delete_misses"`
	// Collisions is a number of happened key-collisions
	// hash 冲突
	Collisions int64 `json:"collisions"`
}
