package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestAll(t *testing.T) {
	t.Run("RedisCache", TestRedisCache)
	t.Run("NewRedisCache", TestNewRedisCache)
}

func TestRedisCache(t *testing.T) {
	ctx := context.Background()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	cache := NewRedisCache(redisClient)

	t.Run("Базовые операции", func(t *testing.T) {
		// Проверяем подключение
		err := cache.client.Ping(ctx).Err()
		assert.NoError(t, err)

		// Тест Get/Delete
		key := "test_key"
		value := "test_value"
		err = cache.client.Set(ctx, key, value, 0).Err()
		assert.NoError(t, err)

		result, err := cache.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, value, result)

		err = cache.Delete(ctx, key)
		assert.NoError(t, err)

		_, err = cache.Get(ctx, key)
		assert.Error(t, err) // Ключ должен быть удален

		// Тест LPush/BRPop
		listKey := "test_list"
		testValue := "test_item"

		cmd := cache.LPush(ctx, listKey, testValue)
		assert.NoError(t, cmd.Err())

		result2, err := cache.BRPop(ctx, 1*time.Second, listKey).Result()
		assert.NoError(t, err)
		assert.Len(t, result2, 2) // BRPop возвращает [key, element]
		assert.Equal(t, testValue, result2[1])
	})

	t.Run("Конструктор", func(t *testing.T) {
		// Проверяем создание экземпляра
		c := NewRedisCache(redisClient)
		assert.NotNil(t, c)
		assert.NotNil(t, c.client)
	})

	t.Run("Get с несуществующим ключом", func(t *testing.T) {
		// Проверяем получение несуществующего ключа
		_, err := cache.Get(ctx, "non_existent_key")
		assert.Error(t, err)
		assert.Equal(t, redis.Nil, err)
	})

	t.Run("LPush множественные значения", func(t *testing.T) {
		listKey := "test_list_multiple"
		// Очищаем список перед тестом
		cache.Delete(ctx, listKey)

		// Проверяем push нескольких значений
		values := []interface{}{"value1", "value2", "value3"}
		cmd := cache.LPush(ctx, listKey, values...)
		assert.NoError(t, cmd.Err())
		assert.Equal(t, int64(3), cmd.Val())

		// Очищаем после теста
		cache.Delete(ctx, listKey)
	})

	t.Run("BRPop таймаут", func(t *testing.T) {
		listKey := "test_list_timeout"

		// Проверяем таймаут при пустом списке
		result := cache.BRPop(ctx, 1*time.Second, listKey)
		assert.Error(t, result.Err())
		assert.Equal(t, redis.Nil, result.Err())
	})

	t.Run("Delete несуществующий ключ", func(t *testing.T) {
		// Проверяем удаление несуществующего ключа
		err := cache.Delete(ctx, "non_existent_key")
		assert.NoError(t, err)
	})
}

func TestNewRedisCache(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	cache := NewRedisCache(redisClient)
	assert.NotNil(t, cache)
	assert.NotNil(t, cache.client)
}
