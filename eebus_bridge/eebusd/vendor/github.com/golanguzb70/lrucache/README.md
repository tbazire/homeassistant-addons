# LRUCache - Generic LRU Cache for Golang
LRUCache is a Go package that provides a simple and efficient LRU (Least Recently Used) caching mechanism with generics support. This package allows you to store and manage data in Go runtime while ensuring that the cache size does not exceed a predefined limit. The LRU algorithm ensures that the most recently used items are retained in the cache while discarding the least recently used ones when the cache reaches its capacity.

# Installation
To use LRUCache, you need to have Go installed on your system. Then, you can install the package using the following go command:
```
go get github.com/golanguzb70/lrucache@latest
```

# Features
* LRU Caching: LRUCache implements the LRU (Least Recently Used) algorithm, which optimizes the cache by keeping the most recently used items, thereby reducing cache misses.
* Generics Support: With Go 1.18 or later, this package utilizes the new generics feature to enable more flexible and type-safe cache usage. This allows you to store any type of data in the cache.
* Timeout: The package also supports deleting data after specific number of seconds, which makes it easy to cache data that is frequently accessed and also updated instantly in database. Thanks to this feature, users always have updated data with amazing speed to get.

# Projects that uses this package
If you use this package, please, don't forget to give logo of your project or add here and create pull request.
<a href="https://uzdplus.uz/">
  <img src="https://github.com/golanguzb70/lrucache/blob/main/src/uzdplus.jpg" alt="Uzdigital" width="200" height="100">
</a>


# Usage
## Import the package
To start using LRUCache, import the package into your Go code:
```
import "github.com/golanguzb70/lrucache"
```

## Create a cache instance
Create a new cache instance with a specific capacity and timeout. To disable timeout just give 0. As it is generic function also provide specific data types for Key and Value:
```
cache := lrucache.New[int, string](100, 0) // Create a cache with a capacity of 100 items and disable timeout. Key is int and value is string. 
```

## Add items to the cache
Use the Put method to insert data into the cache:
```
cache.Put(1, "one") // Key is 1 and value is 'one'. Because cache is created with int key and string value, it accepts these types respectively.
```

## Retrieve items from the cache
You can retrieve items from the cache using the Get method:
```
value, found := cache.Get(1) // Get the data associated with key which 1
if found {
    // Data found in the cache
    // ... handle the data ...
} else {
    // Data not found in the cache
    // ... handle the case ...
}
```
# Contributing
Contributions to LRUCache are welcome! If you find any issues or have improvements in mind, please open an issue or submit a pull request on the GitHub repository. For more information about CONTRIBUTING refer to this [link](https://github.com/golanguzb70/lrucache/blob/main/CONTRIBUTING.md)

# License
GoCache is licensed under the [MIT License](https://en.wikipedia.org/wiki/MIT_License). See the [LICENSE](https://github.com/golanguzb70/lrucache/blob/main/LICENSE) file for more details.

#
Thank you for using LRUCache! We hope this package proves usefull in your Go projects. If you have any questions or need further assistance, feel free to reach out to us. Happy coding!
