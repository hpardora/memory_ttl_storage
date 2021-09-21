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

restored := item.Content.(*TestStructOne)
```

