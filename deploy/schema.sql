-- fc_server 建库建表(与代码中的建表语句一致,可重复执行)
-- 用法:在能访问数据库内网地址的服务器上执行
--   mysql -h <数据库地址> -u <用户名> -p --default-character-set=utf8mb4 < schema.sql

CREATE DATABASE IF NOT EXISTS fc DEFAULT CHARACTER SET utf8mb4;
USE fc;

-- 排行榜最好成绩(每人一行)
CREATE TABLE IF NOT EXISTS user_best_score (
  user_id           VARCHAR(64)  NOT NULL,
  user_name         VARCHAR(64)  NOT NULL DEFAULT '',
  stage             BIGINT       NOT NULL,
  stage_cultivation VARCHAR(320) CHARACTER SET ascii NOT NULL,
  total_cultivation VARCHAR(320) CHARACTER SET ascii NOT NULL,
  score_key         VARCHAR(700) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  achieved_at       BIGINT       NOT NULL,
  client_ip         VARCHAR(45)  NOT NULL DEFAULT '',
  province          VARCHAR(64)  NOT NULL DEFAULT '',
  city              VARCHAR(64)  NOT NULL DEFAULT '',
  district          VARCHAR(64)  NOT NULL DEFAULT '',
  updated_at        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
                                 ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  KEY idx_region_score (province, city, district, score_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 玩家资料(微信身份、昵称头像)
CREATE TABLE IF NOT EXISTS player_profile (
  user_id       VARCHAR(64)   NOT NULL,
  appid         VARCHAR(64)   NOT NULL,
  openid        VARCHAR(128)  NOT NULL,
  nickname      VARCHAR(64)   NOT NULL DEFAULT '',
  avatar_url    VARCHAR(2048) NOT NULL DEFAULT '',
  created_at    BIGINT        NOT NULL,
  last_login_at BIGINT        NOT NULL,
  updated_at    BIGINT        NOT NULL,
  revision      BIGINT        NOT NULL,
  synced_at     TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP
                              ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  UNIQUE KEY uk_appid_openid (appid, openid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- 游戏记录(一人多条,只追加)
CREATE TABLE IF NOT EXISTS player_game_record (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  record_id     CHAR(32)        NOT NULL,
  user_id       VARCHAR(64)     NOT NULL,
  data          JSON            NOT NULL,
  created_at_ms BIGINT          NOT NULL,
  client_ip     VARCHAR(45)     NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_record_id (record_id),
  KEY idx_user_created (user_id, created_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- 成就(每人每项一行,保留首次解锁)
CREATE TABLE IF NOT EXISTS player_achievement (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        VARCHAR(64)     NOT NULL,
  achievement_id VARCHAR(64)     NOT NULL,
  unlocked_at_ms BIGINT          NOT NULL,
  received_at_ms BIGINT          NOT NULL,
  detail         JSON            NULL,
  client_ip      VARCHAR(45)     NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_achievement (user_id, achievement_id),
  KEY idx_achievement (achievement_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
