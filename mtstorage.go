package memory_ttl_storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultTickerTime = time.Second * 1
	defaultTTL        = int64(10)
	defaultShowLogs   = false
	defaultBackupFile = "/opt/memory_ttl_storage/mtstorage.dat"
)

type Item struct {
	Content         interface{}
	ExpireTimestamp int64
	TTL             int64
}

type MemoryTTLStorage struct {
	useBackup  bool
	showLogs   bool
	ticker     time.Ticker
	items      map[string]Item
	defaultTTL int64
	backup     *StorageManager
	mutext     sync.RWMutex
}

type MemoryTTLStoreConfig struct {
	TickerTime time.Duration
	TTLValue   int64
	ShowLogs   bool
	UseBackup  bool
	BackupPath string
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
		if cfg.UseBackup {
			if cfg.BackupPath == "" {
				cfg.BackupPath = defaultBackupFile
			}
			dir := filepath.Dir(cfg.BackupPath)
			err := prepareBackupPath(dir)
			if err != nil {
				log.Println("unable to config backup path", cfg.BackupPath)
			}
		}
		finalShowLogs = cfg.ShowLogs
	}

	rlc := MemoryTTLStorage{
		showLogs:   finalShowLogs,
		defaultTTL: finalTTLValue,
		useBackup:  cfg.UseBackup,
		items:      make(map[string]Item),
	}

	if rlc.useBackup && cfg.BackupPath != "" {
		rlc.backup = NewStorageManager(cfg.BackupPath)
		err := rlc.backup.Restore(&rlc.items)
		if err != nil {
			log.Printf("unable to restore data from backup file: %s", cfg.BackupPath)
		}
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
	if mts.useBackup {
		mts.mutext.Lock()
		defer mts.mutext.Unlock()
		err := mts.backup.Store(mts.items)
		if err != nil {
			log.Println("unable to store data", err)
		}
	}
}

func prepareBackupPath(folder string) error {
	if _, err := os.Stat(folder); os.IsNotExist(err) {
		err := os.Mkdir(folder, os.ModePerm)
		if err != nil {
			return err
		}
	}
	return nil
}

func (mts *MemoryTTLStorage) clearOldEntries() {
	mts.mutext.Lock()
	defer mts.mutext.Unlock()

	for k, v := range mts.items {
		if v.ExpireTimestamp < time.Now().Unix() {
			mts.log("deleting outdated item", k)
			delete(mts.items, k)
		}
	}
	if mts.useBackup{
		mts.backup.Store(mts.items)
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
	mts.mutext.Lock()
	defer mts.mutext.Unlock()

	item := mts.prepareItem(content, ttl)
	mts.items[key] = item
}

func (mts *MemoryTTLStorage) Get(key string) (interface{}, bool) {
	mts.mutext.Lock()
	defer mts.mutext.Unlock()

	val, ok := mts.items[key]
	return val.Content, ok
}

func (mts *MemoryTTLStorage) GetAndRefresh(key string) (interface{}, bool) {
	mts.mutext.Lock()
	defer mts.mutext.Unlock()

	val, ok := mts.items[key]

	item := mts.prepareItem(val.Content, &val.TTL)
	mts.items[key] = item

	return val.Content, ok
}

func (mts *MemoryTTLStorage) Delete(key string) {
	mts.mutext.Lock()
	defer mts.mutext.Unlock()

	delete(mts.items, key)
	mts.log("deleted element with key", key)
}
