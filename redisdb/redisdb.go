package redisdb

import (
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/go-errors/errors"
)

const (
	redisHashKey   = "ytsync"
	redisSyncedVal = "t"
)

type DB struct {
	pool *redis.Pool
}

func New() *DB {
	var r DB
	r.pool = &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 5 * time.Minute,
		Dial:        func() (redis.Conn, error) { return redis.Dial("tcp", ":6379") },
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}
	return &r
}

func (r DB) IsPublished(id string) (bool, error) {
	conn := r.pool.Get()
	defer conn.Close()

	alreadyPublished, err := redis.String(conn.Do("HGET", redisHashKey, id))
	if err != nil && err != redis.ErrNil {
		return false, errors.WrapPrefix(err, "redis error", 0)

	}

	if alreadyPublished == redisSyncedVal {
		return true, nil
	}

	return false, nil
}

func (r DB) SetPublished(id string) error {
	conn := r.pool.Get()
	defer conn.Close()

	_, err := redis.Bool(conn.Do("HSET", redisHashKey, id, redisSyncedVal))
	if err != nil {
		return errors.New("redis error: " + err.Error())
	}
	return nil
}
