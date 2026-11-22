module github.com/hpardora/keepcache/bench

go 1.24

replace github.com/hpardora/keepcache => ../

require (
	github.com/hpardora/keepcache v0.0.0-20210926101522-faaba3a29947
	github.com/jellydator/ttlcache/v3 v3.4.1
	github.com/patrickmn/go-cache v2.1.0+incompatible
)

require golang.org/x/sync v0.16.0 // indirect
