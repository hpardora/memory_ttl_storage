# memory_ttl_storage
Its a problem/solution project.

You can store any struct with a TTL and retrieve by key.

## Basic Usage
To work with default values
```go
type Example struct {}

mts := New(nil)
mts.Add("key",&Example{}, nil)

item, ok := mts.Get("key")
if !ok {
    t.Error("cannot retrieve from service")
}

restored := item.Content.(*Example)
```

Start with custom config
```go
cfg := &MemoryTTLStoreConfig{
	TickerTime: *time.Duration  // How often the ticker ticks
	TTLValue   int64            // Default number of seconds for items TTL 
	ShowLogs   bool             // if you what to show basic logs...
}
mts := New(cfg)
```

## Current Functions
```go
func Add(key string, content interface{}, ttl *int64)
func Get(key string) (*Item, bool)
func GetAndRefresh(key string) (*Item, bool)
func Delete(key string) 
```