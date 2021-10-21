package bigcache

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/allegro/bigcache/v3/queue"
)

type onRemoveCallback func(wrappedEntry []byte, reason RemoveReason)

// Metadata contains information of a specific entry
// 元信息
type Metadata struct {
	RequestCount uint32
}

// 分片
type cacheShard struct {
    // hash
	hashmap     map[uint64]uint32

    // 实体数组
	entries     queue.BytesQueue

    // 读写锁
	lock        sync.RWMutex

    // 实体缓存
	entryBuffer []byte
	// 回调函数
	onRemove    onRemoveCallback

    // 打印详情
	isVerbose    bool

    // 开启统计
	statsEnabled bool

    // 日志函数
	logger       Logger
	// 时钟函数
	clock        clock

    // 超时时间
	lifeWindow   uint64

    // 统计信息
	hashmapStats map[uint64]uint32
	stats        Stats
}

func (s *cacheShard) getWithInfo(key string, hashedKey uint64) (entry []byte, resp Response, err error) {
    // 当前时间
	currentTime := uint64(s.clock.Epoch())

    // 找hash 实体
	s.lock.RLock()
	wrappedEntry, err := s.getWrappedEntry(hashedKey)
	if err != nil {
		s.lock.RUnlock()
		// 查找失败
		return nil, resp, err
	}

    //  取key 值
	if entryKey := readKeyFromEntry(wrappedEntry); key != entryKey {
		s.lock.RUnlock()
		// hash 冲突
		s.collision()
		if s.isVerbose {
		    // 错误日志
			s.logger.Printf("Collision detected. Both %q and %q have the same hash %x", key, entryKey, hashedKey)
		}
		return nil, resp, ErrEntryNotFound
	}

    // 读取实体和数据时间
	entry = readEntry(wrappedEntry)
	oldestTimeStamp := readTimestampFromEntry(wrappedEntry)
	s.lock.RUnlock()

    // 统计收集
	s.hit(hashedKey)

    // 过期判断
	if currentTime-oldestTimeStamp >= s.lifeWindow {
		resp.EntryStatus = Expired
	}

	return entry, resp, nil
}

func (s *cacheShard) get(key string, hashedKey uint64) ([]byte, error) {
    // 找hash 实体
	s.lock.RLock()
	wrappedEntry, err := s.getWrappedEntry(hashedKey)
	if err != nil {
		s.lock.RUnlock()
		return nil, err
	}

    // hash 冲突检测
	if entryKey := readKeyFromEntry(wrappedEntry); key != entryKey {
		s.lock.RUnlock()
		s.collision()
		if s.isVerbose {
			s.logger.Printf("Collision detected. Both %q and %q have the same hash %x", key, entryKey, hashedKey)
		}
		return nil, ErrEntryNotFound
	}

    // 抽取实体
	entry := readEntry(wrappedEntry)
	s.lock.RUnlock()

    // 统计收集
	s.hit(hashedKey)

	return entry, nil
}

func (s *cacheShard) getWrappedEntry(hashedKey uint64) ([]byte, error) {
    // 查映射表
	itemIndex := s.hashmap[hashedKey]

	if itemIndex == 0 {
	    // 无此hash
		s.miss()
		return nil, ErrEntryNotFound
	}

	// 取实体
	wrappedEntry, err := s.entries.Get(int(itemIndex))
	if err != nil {
		s.miss()
		return nil, err
	}

	return wrappedEntry, err
}

func (s *cacheShard) getValidWrapEntry(key string, hashedKey uint64) ([]byte, error) {
    // 查询
	wrappedEntry, err := s.getWrappedEntry(hashedKey)
	if err != nil {
		return nil, err
	}

	// 对比key
	if !compareKeyFromEntry(wrappedEntry, key) {
		// hash 冲突
		s.collision()
		if s.isVerbose {
			s.logger.Printf("Collision detected. Both %q and %q have the same hash %x", key, readKeyFromEntry(wrappedEntry), hashedKey)
		}

		return nil, ErrEntryNotFound
	}
	s.hitWithoutLock(hashedKey)

	return wrappedEntry, nil
}

func (s *cacheShard) set(key string, hashedKey uint64, entry []byte) error {
	// 取当前时间
	currentTimestamp := uint64(s.clock.Epoch())

	s.lock.Lock()

	// 找到老key，清理掉
	if previousIndex := s.hashmap[hashedKey]; previousIndex != 0 {
		if previousEntry, err := s.entries.Get(int(previousIndex)); err == nil {
			resetKeyFromEntry(previousEntry)
			//remove hashkey
			delete(s.hashmap, hashedKey)
		}
	}

	// 触发淘汰
	if oldestEntry, err := s.entries.Peek(); err == nil {
		s.onEvict(oldestEntry, currentTimestamp, s.removeOldestEntry)
	}

	// 打包
	w := wrapEntry(currentTimestamp, hashedKey, key, entry, &s.entryBuffer)

	for {
		// 写值
		if index, err := s.entries.Push(w); err == nil {
			s.hashmap[hashedKey] = uint32(index)
			s.lock.Unlock()
			return nil
		}

		// 写不成功，清理一下空间
		if s.removeOldestEntry(NoSpace) != nil {
			s.lock.Unlock()
			return fmt.Errorf("entry is bigger than max shard size")
		}
	}
}

func (s *cacheShard) addNewWithoutLock(key string, hashedKey uint64, entry []byte) error {
	// 当前时间
	currentTimestamp := uint64(s.clock.Epoch())

	// 触发淘汰
	if oldestEntry, err := s.entries.Peek(); err == nil {
		s.onEvict(oldestEntry, currentTimestamp, s.removeOldestEntry)
	}

    // 打包
	w := wrapEntry(currentTimestamp, hashedKey, key, entry, &s.entryBuffer)

	// 写值，如果不成功清理缓存
	for {
		if index, err := s.entries.Push(w); err == nil {
			s.hashmap[hashedKey] = uint32(index)
			return nil
		}
		if s.removeOldestEntry(NoSpace) != nil {
			return fmt.Errorf("entry is bigger than max shard size")
		}
	}
}

func (s *cacheShard) setWrappedEntryWithoutLock(currentTimestamp uint64, w []byte, hashedKey uint64) error {
	// 清理老key
	if previousIndex := s.hashmap[hashedKey]; previousIndex != 0 {
		if previousEntry, err := s.entries.Get(int(previousIndex)); err == nil {
			resetKeyFromEntry(previousEntry)
		}
	}

    // 触发老值
	if oldestEntry, err := s.entries.Peek(); err == nil {
		s.onEvict(oldestEntry, currentTimestamp, s.removeOldestEntry)
	}

    // 写新值
	for {
		if index, err := s.entries.Push(w); err == nil {
			s.hashmap[hashedKey] = uint32(index)
			return nil
		}
		if s.removeOldestEntry(NoSpace) != nil {
			return fmt.Errorf("entry is bigger than max shard size")
		}
	}
}

func (s *cacheShard) append(key string, hashedKey uint64, entry []byte) error {
	s.lock.Lock()
	// 打实体
	wrappedEntry, err := s.getValidWrapEntry(key, hashedKey)

	if err == ErrEntryNotFound {
	    // 找不到，新增一个
		err = s.addNewWithoutLock(key, hashedKey, entry)
		s.lock.Unlock()
		return err
	}
	if err != nil {
	    // 出错了
		s.lock.Unlock()
		return err
	}

	currentTimestamp := uint64(s.clock.Epoch())

    // buffer 追加
	w := appendToWrappedEntry(currentTimestamp, wrappedEntry, entry, &s.entryBuffer)

    // 更新
	err = s.setWrappedEntryWithoutLock(currentTimestamp, w, hashedKey)
	s.lock.Unlock()

	return err
}

func (s *cacheShard) del(hashedKey uint64) error {
	// Optimistic pre-check using only readlock
	// 加锁
	s.lock.RLock()
	{
		itemIndex := s.hashmap[hashedKey]

		if itemIndex == 0 {
		    // 没有这个项目
			s.lock.RUnlock()
			s.delmiss()
			return ErrEntryNotFound
		}

		// 获取实体
		if err := s.entries.CheckGet(int(itemIndex)); err != nil {
			// 获取失败
			s.lock.RUnlock()
			s.delmiss()
			return err
		}
	}
	s.lock.RUnlock()

	s.lock.Lock()
	{
		// After obtaining the writelock, we need to read the same again,
		// since the data delivered earlier may be stale now
		itemIndex := s.hashmap[hashedKey]

		if itemIndex == 0 {
		    // 二次校验
			s.lock.Unlock()
			s.delmiss()
			return ErrEntryNotFound
		}

		// 获取实体
		wrappedEntry, err := s.entries.Get(int(itemIndex))
		if err != nil {
			s.lock.Unlock()
			s.delmiss()
			return err
		}

		// 删key，触发淘汰
		delete(s.hashmap, hashedKey)
		s.onRemove(wrappedEntry, Deleted)
		// 统计一下
		if s.statsEnabled {
			delete(s.hashmapStats, hashedKey)
		}
		// 重置key
		resetKeyFromEntry(wrappedEntry)
	}
	s.lock.Unlock()

	s.delhit()
	return nil
}

// 触发淘汰
func (s *cacheShard) onEvict(oldestEntry []byte, currentTimestamp uint64, evict func(reason RemoveReason) error) bool {
	oldestTimestamp := readTimestampFromEntry(oldestEntry)
	if currentTimestamp-oldestTimestamp > s.lifeWindow {
		evict(Expired)
		return true
	}
	return false
}

func (s *cacheShard) cleanUp(currentTimestamp uint64) {
	s.lock.Lock()
	for {
		if oldestEntry, err := s.entries.Peek(); err != nil {
		    // 打老的失败
			break
		} else if evicted := s.onEvict(oldestEntry, currentTimestamp, s.removeOldestEntry); !evicted {
		    // 触发淘汰，再看是否继续
			break
		}
	}
	s.lock.Unlock()
}

func (s *cacheShard) getEntry(hashedKey uint64) ([]byte, error) {
	s.lock.RLock()

	// 找实体
	entry, err := s.getWrappedEntry(hashedKey)

	// copy entry
	// 复制实体
	newEntry := make([]byte, len(entry))
	copy(newEntry, entry)

	s.lock.RUnlock()

	return newEntry, err
}

// 复制hashkeys
func (s *cacheShard) copyHashedKeys() (keys []uint64, next int) {
	s.lock.RLock()
	keys = make([]uint64, len(s.hashmap))

	for key := range s.hashmap {
		keys[next] = key
		next++
	}

	s.lock.RUnlock()
	return keys, next
}

// 清理老实体
func (s *cacheShard) removeOldestEntry(reason RemoveReason) error {
    // 弹出最老的
	oldest, err := s.entries.Pop()
	if err == nil {
	    // 获取其hash
		hash := readHashFromEntry(oldest)
		if hash == 0 {
			// entry has been explicitly deleted with resetKeyFromEntry, ignore
			return nil
		}
		// 删除hashkey
		delete(s.hashmap, hash)
		// 删除回调
		s.onRemove(oldest, reason)
		// 统计处理
		if s.statsEnabled {
			delete(s.hashmapStats, hash)
		}
		return nil
	}
	return err
}

func (s *cacheShard) reset(config Config) {
	s.lock.Lock()
	// 重置hashkey
	s.hashmap = make(map[uint64]uint32, config.initialShardSize())
	// buffer
	s.entryBuffer = make([]byte, config.MaxEntrySize+headersSizeInBytes)
	s.entries.Reset()
	s.lock.Unlock()
}

// 获取hash长度
func (s *cacheShard) len() int {
	s.lock.RLock()
	res := len(s.hashmap)
	s.lock.RUnlock()
	return res
}

// 容量
func (s *cacheShard) capacity() int {
	s.lock.RLock()
	res := s.entries.Capacity()
	s.lock.RUnlock()
	return res
}

// 获取统计信息
func (s *cacheShard) getStats() Stats {
	var stats = Stats{
		Hits:       atomic.LoadInt64(&s.stats.Hits),
		Misses:     atomic.LoadInt64(&s.stats.Misses),
		DelHits:    atomic.LoadInt64(&s.stats.DelHits),
		DelMisses:  atomic.LoadInt64(&s.stats.DelMisses),
		Collisions: atomic.LoadInt64(&s.stats.Collisions),
	}
	return stats
}

// 获取hashkey 请求数
func (s *cacheShard) getKeyMetadataWithLock(key uint64) Metadata {
	s.lock.RLock()
	c := s.hashmapStats[key]
	s.lock.RUnlock()
	return Metadata{
		RequestCount: c,
	}
}

func (s *cacheShard) getKeyMetadata(key uint64) Metadata {
	return Metadata{
		RequestCount: s.hashmapStats[key],
	}
}

// 统计命中数
func (s *cacheShard) hit(key uint64) {
	atomic.AddInt64(&s.stats.Hits, 1)
	if s.statsEnabled {
	    // key 的请求数
		s.lock.Lock()
		s.hashmapStats[key]++
		s.lock.Unlock()
	}
}

func (s *cacheShard) hitWithoutLock(key uint64) {
	atomic.AddInt64(&s.stats.Hits, 1)
	if s.statsEnabled {
		s.hashmapStats[key]++
	}
}

func (s *cacheShard) miss() {
	atomic.AddInt64(&s.stats.Misses, 1)
}

func (s *cacheShard) delhit() {
	atomic.AddInt64(&s.stats.DelHits, 1)
}

func (s *cacheShard) delmiss() {
	atomic.AddInt64(&s.stats.DelMisses, 1)
}

func (s *cacheShard) collision() {
	atomic.AddInt64(&s.stats.Collisions, 1)
}

func initNewShard(config Config, callback onRemoveCallback, clock clock) *cacheShard {
	// 参数计算
	bytesQueueInitialCapacity := config.initialShardSize() * config.MaxEntrySize
	maximumShardSizeInBytes := config.maximumShardSizeInBytes()
	if maximumShardSizeInBytes > 0 && bytesQueueInitialCapacity > maximumShardSizeInBytes {
		bytesQueueInitialCapacity = maximumShardSizeInBytes
	}

	// 构造函数
	return &cacheShard{
		hashmap:      make(map[uint64]uint32, config.initialShardSize()),
		hashmapStats: make(map[uint64]uint32, config.initialShardSize()),
		entries:      *queue.NewBytesQueue(bytesQueueInitialCapacity, maximumShardSizeInBytes, config.Verbose),
		entryBuffer:  make([]byte, config.MaxEntrySize+headersSizeInBytes),
		onRemove:     callback,

		isVerbose:    config.Verbose,
		logger:       newLogger(config.Logger),
		clock:        clock,
		lifeWindow:   uint64(config.LifeWindow.Seconds()),
		statsEnabled: config.StatsEnabled,
	}
}
