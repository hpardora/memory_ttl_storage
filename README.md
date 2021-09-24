# memory_ttl_storage
Its a problem/solution project.

You can store any struct with a TTL and retrieve by key.

## Current Functions
```go
func Add(key string, content interface{}, ttl *int64)
func Get(key string) (interface{}, bool)
func GetAndRefresh(key string) (interface{}, bool)
func Delete(key string) 
```

## Basic Usage

You can import package with
```go
import "github.com/hpardora/memory_ttl_storage"
```

To work with default values
```go
type Example struct {}

mts := New(nil)
mts.Add("key",&Example{}, nil)

item, ok := mts.Get("key")
if !ok {
    t.Error("cannot retrieve from service")
}

restored := item.(*Example)
```

Start with custom config
```go
cfg := &MemoryTTLStoreConfig{
	TickerTime: *time.Duration  // How often the ticker ticks
	TTLValue   int64            // Default number of seconds for items TTL 
	ShowLogs   bool             // if you what to show basic logs...
	UseBackup  bool             // true for save cache data on Stop(), and restore on startup
	BackupPath string           // by default /opt/memory_ttl_storage/mtstorage.dat
}
mts := New(cfg)
```
> :warning: **If you want to store custom structs, you must add the following code: gob.Register(YourStruct{})**

Update the DefaultTTL
```go
mts := New(nil)
mts.SetDefaultTTL(100)
```
