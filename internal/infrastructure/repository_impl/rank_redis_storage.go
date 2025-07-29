package repository_impl

import (
	"context"

	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"

	"github.com/redis/go-redis/v9"
)

type RankRedisStorage struct {
	redisClient *redis.Client
}

var (
	rankRedisStorage *RankRedisStorage
	_                repository.RankStorage = (*RankRedisStorage)(nil)
)

func Init() {
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
	ctx := context.Background()
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		panic("Failed to connect to Redis for rank: " + err.Error())
	}

	rankRedisStorage = &RankRedisStorage{
		redisClient: redisClient,
	}
}

func GetRankRedisStorage() *RankRedisStorage {
	if rankRedisStorage == nil {
		panic("rankRedisStorage is not initialized")
	}
	return rankRedisStorage
}

func (r *RankRedisStorage) Get(ctx context.Context, location *vo.Location, limit int) (map[string][]repository.KeyScore, error) {
	pipe := r.redisClient.Pipeline()

	result := make(map[string][]repository.KeyScore)
	keys := []string{location.Province, location.City, location.District}
	for _, key := range keys {
		pipe.ZRevRangeWithScores(ctx, key, 0, int64(limit-1))
	}

	pipelinResults, err := pipe.Exec(ctx)
	if err != nil {
		return nil, err
	}

	for i, pipelineResult := range pipelinResults {
		if cmd, ok := pipelineResult.(*redis.ZSliceCmd); ok {
			zs, err := cmd.Result()
			if err == nil {
				var items []repository.KeyScore
				for _, z := range zs {
					items = append(items, repository.KeyScore{
						Key:   z.Member.(string),
						Score: int(z.Score),
					})
				}
				if i < len(keys) {
					result[keys[i]] = items
				}
			}
		}
	}

	return result, nil
}

func (r *RankRedisStorage) Upload(ctx context.Context, location *vo.Location, key string, score int) error {
	pipe := r.redisClient.Pipeline()
	pipe.ZAdd(ctx, location.Province, redis.Z{
		Score:  float64(score),
		Member: key,
	})
	pipe.ZAdd(ctx, location.City, redis.Z{
		Score:  float64(score),
		Member: key,
	})
	pipe.ZAdd(ctx, location.District, redis.Z{
		Score:  float64(score),
		Member: key,
	})

	_, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}

	results, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Err() != nil {
			return result.Err()
		}
	}

	return nil
}
