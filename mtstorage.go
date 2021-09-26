package memory_ttl_storage

import (
	"encoding/gob"
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
	mutext          sync.RWMutex
	useBackup       bool
	showLogs        bool
	items           map[string]Item
	defaultTTL      int64
	cleaningTicker  time.Ticker
	backupTicker    time.Ticker
	backup          *StorageManager
	onBackupProcess bool
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
			rlc.log("unable to restore data from backup file: %s", cfg.BackupPath)
		}
		rlc.backupTicker = *rlc.NewBackupTicker()
	}

	rlc.log(fmt.Sprintf("creating a MemoryTTLStorage with tickerTime %d/s and default TTL %d/s", finalTickerTime/time.Second, finalTTLValue))
	rlc.cleaningTicker = *rlc.NewCleanerTicker(finalTickerTime)

	rlc.RegisterInterface(Item{})
	return &rlc
}

func (mts *MemoryTTLStorage) NewCleanerTicker(tickerTime time.Duration) *time.Ticker {
	t := time.NewTicker(tickerTime)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				mts.clearOldEntries()
			case <-quit:
				t.Stop()
				return
			}
		}
	}()
	return t
}

func (mts *MemoryTTLStorage) NewBackupTicker() *time.Ticker {
	t := time.NewTicker(time.Second * 5)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				if !mts.onBackupProcess {
					mts.onBackupProcess = true

					toStore := make(map[string]Item)

					mts.mutext.Lock()
					for k, v := range mts.items {
						toStore[k] = v
					}
					mts.mutext.Unlock()

					err := mts.backup.Store(mts.items)
					if err != nil {
						mts.log("unable create a timed backup", err)
					}
					mts.onBackupProcess = false
				}
			case <-quit:
				t.Stop()
				return
			}
		}
	}()
	return t
}

func (mts *MemoryTTLStorage) Stop() {
	mts.cleaningTicker.Stop()
	if mts.useBackup {
		mts.mutext.Lock()
		defer mts.mutext.Unlock()
		defer mts.backupTicker.Stop()
		err := mts.backup.Store(mts.items)
		if err != nil {
			mts.log("unable to store data", err)
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
}

func (mts *MemoryTTLStorage) log(v ...interface{}) {
	if mts.showLogs {
		data := v
		log.Println(data)
	}
}

func (mts *MemoryTTLStorage) RegisterInterface(i interface{}) {
	gob.Register(i)
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
