package lrucache

import (
	"time"
)

type Node[T any, K comparable] struct {
	key     K
	value   T
	created time.Time
	prev,
	next *Node[T, K]
}

type LRUCache[K comparable, T any] struct {
	space      int
	cache      map[K]*Node[T, K]
	head, tail *Node[T, K]
	timeout    int
}

func New[K comparable, T any](capacity, timeout int) LRUCache[K, T] {
	cache := map[K]*Node[T, K]{}
	var (
		zero    T
		zeroKey K
	)
	head, tail := &Node[T, K]{zeroKey, zero, time.Now(), nil, nil}, &Node[T, K]{zeroKey, zero, time.Now(), nil, nil}
	head.next = tail
	tail.prev = head
	return LRUCache[K, T]{capacity, cache, head, tail, timeout}
}

func (lru *LRUCache[K, T]) AddNode(node *Node[T, K]) {
	node.next = lru.head.next
	node.prev = lru.head
	node.next.prev = node
	lru.head.next = node
}

func (lru *LRUCache[K, T]) RemoveNode(node *Node[T, K]) {
	prev, next := node.prev, node.next
	node.prev.next = next
	node.next.prev = prev
}

func (lru *LRUCache[K, T]) Get(key K) (T, bool) {
	var res T
	if node, ok := lru.cache[key]; ok {
		if lru.timeout > 0 {
			if node.created.Add(time.Second * time.Duration(lru.timeout)).Before(time.Now()) {
				lru.RemoveNode(node)
				delete(lru.cache, key)
				lru.space++
				return res, false
			}
		}
		lru.RemoveNode(node)
		lru.AddNode(node)
		return node.value, true
	} else {
		return res, false
	}
}

func (lru *LRUCache[K, T]) Put(key K, value T) {
	if node, ok := lru.cache[key]; ok {
		node.value = value
		lru.RemoveNode(node)
		lru.AddNode(node)
	} else {
		if lru.space == 0 {
			toRemove := lru.tail.prev
			lru.RemoveNode(toRemove)
			delete(lru.cache, toRemove.key)
		} else {
			lru.space--
		}
		newNode := &Node[T, K]{key, value, time.Now(), nil, nil}
		lru.AddNode(newNode)
		lru.cache[key] = newNode
	}
}

func (lru *LRUCache[K, T]) Clear() {
	for k := range lru.cache {
		delete(lru.cache, k)
	}
	var (
		zero    T
		zeroKey K
	)
	head, tail := &Node[T, K]{zeroKey, zero, time.Now(), nil, nil}, &Node[T, K]{zeroKey, zero, time.Now(), nil, nil}
	head.next = tail
	tail.prev = head
}
