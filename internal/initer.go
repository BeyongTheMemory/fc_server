package internal

import (
	"fc_server/internal/domain/rank/application/impl"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"
)

func Init() {
	repository_impl.Init()
	impl.Init(repository_impl.GetRankRedisStorage())
	util.InitLogger()
}
