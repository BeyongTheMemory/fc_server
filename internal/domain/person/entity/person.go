package entity

import (
	"time"
	
	"fc_server/internal/domain/person/entity/vo"
)

type Person struct {
	ID            int64
	Name          string
	CreatedTime   int64
	LastLoginTime int64
	OngoingGameMeta *vo.GameMeta
}

func (p *Person) Create() {
	p.CreatedTime = time.Now().Unix()
}

func (p *Person) Login() {
	p.LastLoginTime = time.Now().Unix()
}
