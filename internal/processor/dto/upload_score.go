package dto

type UploadScoreRequest struct {
	UserInfo *UserInfoDto `json:"user_info"`
	Score    int          `json:"score"`
}

type UploadScoreResponse struct {
	
}