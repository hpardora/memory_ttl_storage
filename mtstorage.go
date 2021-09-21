package memory_ttl_storage

import (
	"fmt"
	"log"
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

	if cfg != nil{
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

func (r *MemoryTTLStorage) Stop() {
	r.ticker.Stop()
}

func (r *MemoryTTLStorage) clearOldEntries() {
	for k, v := range r.items {
		if v.ExpireTimestamp < time.Now().Unix() {
			r.log("deleting outdated item", k)
			delete(r.items, k)
		}
	}
}

func (r *MemoryTTLStorage) log(v ...interface{}) {
	if r.showLogs {
		data := v
		log.Println(data)
	}
}

func (r *MemoryTTLStorage) SetDefaultTTL(defaultTTL int64) {
	r.defaultTTL = defaultTTL
	r.log("defaultTTL updated", defaultTTL)
}

func (r *MemoryTTLStorage) Get(key string) (*Item, bool) {
	val, ok := r.items[key]
	return &val, ok
}

func (r *MemoryTTLStorage) GetAndRefresh(key string) (*Item, bool) {
	val, ok := r.items[key]
	r.Add(key, val.Content, &val.TTL)
	return &val, ok
}

func (r *MemoryTTLStorage) Delete(key string) {
	delete(r.items, key)
	r.log("deleted element with key", key)
}

func (r *MemoryTTLStorage) Add(key string, content interface{}, ttl *int64) {
	finalTTL := r.defaultTTL
	if ttl != nil {
		finalTTL = *ttl
	}

	expirationTS := time.Now().Unix() + finalTTL
	i := Item{
		Content:         content,
		ExpireTimestamp: expirationTS,
		TTL:             finalTTL,
	}
	r.items[key] = i
}
