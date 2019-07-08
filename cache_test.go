package main

import "testing"

func TestCacheInterface(t *testing.T) {
	var _ cache = new(memoryCache)
}

func TestMemoryCache(t *testing.T) {
	t.Run("Get", func(t *testing.T) {

	})
	t.Run("Put", func(t *testing.T) {

	})
	t.Run("Limit", func(t *testing.T) {

	})
	t.Run("Expiry", func(t *testing.T) {

	})
	t.Run("Clean", func(t *testing.T) {

	})
	// TODO
}
