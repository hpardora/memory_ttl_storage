package memory_ttl_storage

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	defaultTickerTime = time.Second * 1
	defaultTTL        = int64(10)
    defaultShowLogs   = false
)

type Item struct {
	Content         interface{}
	ExpireTimestamp int64
	TTL             int64
}

type MemoryTTLStorage struct {
	showLogs   bool
	ticker     time.Ticker
	items      map[string]Item
	defaultTTL int64
	mu         sync.RWMutex
}

type MemoryTTLStoreConfig struct {
	TickerTime time.Duration
	TTLValue   int64
	ShowLogs   bool
}

func New(cfg *MemoryTTLStoreConfig) *MemoryTTLStorage {
	finalTickerTime := defaultTickerTime
	finalTTLValue := defaultTTL
	finalShowLogs := defaultShowLogs

	if cfg != nil {
		if cfg.TickerTime != 0 {
			finalTickerTime = cfg.TickerTime
		}
		if cfg.TTLValue != 0 {
			finalTTLValue = cfg.TTLValue
		}
		finalShowLogs = cfg.ShowLogs
	}

	rlc := MemoryTTLStorage{
		showLogs:   finalShowLogs,
		defaultTTL: finalTTLValue,
		items:      make(map[string]Item),
	}

	rlc.log(fmt.Sprintf("creating a MemoryTTLStorage with tickerTime %d/s and default TTL %d/s", finalTickerTime/time.Second, finalTTLValue))

	t := time.NewTicker(finalTickerTime)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				rlc.clearOldEntries()
			case <-quit:
				t.Stop()
				return
			}
		}
	}()
	rlc.ticker = *t
	return &rlc
}

func (mts *MemoryTTLStorage) Stop() {
	mts.ticker.Stop()
}

func (mts *MemoryTTLStorage) clearOldEntries() {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	for k, v := range mts.items {
		if v.ExpireTimestamp < time.Now().Unix() {
			mts.log("deleting outdated item", k)
			delete(mts.items, k)
		}
	}
}

func (mts *MemoryTTLStorage) log(v ...interface{}) {
	if mts.showLogs {
		data := v
		log.Println(data)
	}
}

func (mts *MemoryTTLStorage) SetDefaultTTL(defaultTTL int64) {
	mts.defaultTTL = defaultTTL
	mts.log("defaultTTL updated", defaultTTL)
}

func (mts *MemoryTTLStorage) prepareItem(content interface{}, ttl *int64) Item {
	finalTTL := mts.defaultTTL
	if ttl != nil {
		finalTTL = *ttl
	}

	expirationTS := time.Now().Unix() + finalTTL
	item := Item{
		Content:         content,
		ExpireTimestamp: expirationTS,
		TTL:             finalTTL,
	}
	return item
}

func (mts *MemoryTTLStorage) Add(key string, content interface{}, ttl *int64) {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	item := mts.prepareItem(content, ttl)
	mts.items[key] = item
}

func (mts *MemoryTTLStorage) Get(key string) (interface{}, bool) {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	val, ok := mts.items[key]
	return val.Content, ok
}

func (mts *MemoryTTLStorage) GetAndRefresh(key string) (interface{}, bool) {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	val, ok := mts.items[key]

	item := mts.prepareItem(val.Content, &val.TTL)
	mts.items[key] = item

	return val.Content, ok
}

func (mts *MemoryTTLStorage) Delete(key string) {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	delete(mts.items, key)
	mts.log("deleted element with key", key)
}
