package bigcache

import (
	"sync"
)

// 迭代器错误
type iteratorError string
func (e iteratorError) Error() string {
	return string(e)
}

// ErrInvalidIteratorState is reported when iterator is in invalid state
// 状态错误
const ErrInvalidIteratorState = iteratorError("Iterator is in invalid state. Use SetNext() to move to next position")

// ErrCannotRetrieveEntry is reported when entry cannot be retrieved from underlying
// 获取不到实体
const ErrCannotRetrieveEntry = iteratorError("Could not retrieve entry from cache")

// 空实体
var emptyEntryInfo = EntryInfo{}

// EntryInfo holds informations about entry in the cache
type EntryInfo struct {
    // 时间、hash、key、值、错误
	timestamp uint64
	hash      uint64
	key       string
	value     []byte
	err       error
}

// Key returns entry's underlying key
// 取key
func (e EntryInfo) Key() string {
	return e.key
}

// Hash returns entry's hash value
func (e EntryInfo) Hash() uint64 {
	return e.hash
}

// Timestamp returns entry's timestamp (time of insertion)
func (e EntryInfo) Timestamp() uint64 {
	return e.timestamp
}

// Value returns entry's underlying value
func (e EntryInfo) Value() []byte {
	return e.value
}

// EntryInfoIterator allows to iterate over entries in the cache
// 实体迭代器
type EntryInfoIterator struct {
    // 锁
	mutex            sync.Mutex
	// 缓存
	cache            *BigCache
	// 当前分片
	currentShard     int
	// 当前序号
	currentIndex     int
	// 当前实体
	currentEntryInfo EntryInfo
	// hash列表
	elements         []uint64
	elementsCount    int
	// 状态
	valid            bool
}

// SetNext moves to next element and returns true if it exists.
func (it *EntryInfoIterator) SetNext() bool {
	// 加锁
	it.mutex.Lock()

    // 向前走
	it.valid = false
	it.currentIndex++

	if it.elementsCount > it.currentIndex {
	    // 取值
		it.valid = true
		empty := it.setCurrentEntry()
		it.mutex.Unlock()

        // 如果为空值，继续向前
		if empty {
			return it.SetNext()
		}

        // 取到值
		return true
	}

    // 向下一个分片找
	for i := it.currentShard + 1; i < it.cache.config.Shards; i++ {
	    // 取分片所有信息
		it.elements, it.elementsCount = it.cache.shards[i].copyHashedKeys()

		// Non empty shard - stick with it
		if it.elementsCount > 0 {
		    // 取第一个元素
			it.currentIndex = 0
			it.currentShard = i
			it.valid = true

			empty := it.setCurrentEntry()
			it.mutex.Unlock()

            // 如果为空继续找，否则成功
			if empty {
				return it.SetNext()
			}
			return true
		}
	}
	it.mutex.Unlock()

    // 没有找到
	return false
}

// 取当前实体
func (it *EntryInfoIterator) setCurrentEntry() bool {
	var entryNotFound = false

	// 取当前实体
	entry, err := it.cache.shards[it.currentShard].getEntry(it.elements[it.currentIndex])

	if err == ErrEntryNotFound {
	    // 空实体
		it.currentEntryInfo = emptyEntryInfo
		entryNotFound = true
	} else if err != nil {
	    // 发现了错误
		it.currentEntryInfo = EntryInfo{
			err: err,
		}
	} else {
	    // 取到实体
		it.currentEntryInfo = EntryInfo{
			timestamp: readTimestampFromEntry(entry),
			hash:      readHashFromEntry(entry),
			key:       readKeyFromEntry(entry),
			value:     readEntry(entry),
			err:       err,
		}
	}

	return entryNotFound
}

// 创建迭代器
func newIterator(cache *BigCache) *EntryInfoIterator {
    // 复制分片0 的所有hash
	elements, count := cache.shards[0].copyHashedKeys()

	return &EntryInfoIterator{
		cache:         cache,
		currentShard:  0,
		currentIndex:  -1,
		elements:      elements,
		elementsCount: count,
	}
}

// Value returns current value from the iterator
// 取迭代器的值
func (it *EntryInfoIterator) Value() (EntryInfo, error) {
	if !it.valid {
		return emptyEntryInfo, ErrInvalidIteratorState
	}

	return it.currentEntryInfo, it.currentEntryInfo.err
}
