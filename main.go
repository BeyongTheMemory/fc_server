package main

import (
	"fc_server/internal/util"
	"net/http"

	"github.com/gin-gonic/gin"

	"fc_server/internal"
	"fc_server/internal/processor"
	"fc_server/internal/processor/dto"
)

//// fetchRankHandler handles requests to /fetch_rank
//func fetchRankHandler(w http.ResponseWriter, r *http.Request) {
//	var request *dto.FetchRankListRequest
//	if err := json.NewDecoder(r.Body).Decode(request); err != nil {
//		log.Println("Failed to decode request:", err)
//		handleError(w, err, http.StatusBadRequest)
//		return
//	}
//	response, err := processor.FetchRankList(context.Background(), request)
//	if err != nil {
//		handleError(w, err, http.StatusInternalServerError)
//		return
//	}
//	handleSuccess(w, response)
//}
//
//func uploadScoreHandler(w http.ResponseWriter, r *http.Request) {
//	var request *dto.UploadScoreRequest
//	if err := json.NewDecoder(r.Body).Decode(request); err != nil {
//		log.Println("Failed to decode request:", err)
//		handleError(w, err, http.StatusBadRequest)
//		return
//	}
//
//	response, err := processor.UploadScore(context.Background(), request)
//	if err != nil {
//		handleError(w, err, http.StatusInternalServerError)
//		return
//	}
//	handleSuccess(w, response)
//}

//func handleError(w http.ResponseWriter, err error, statusCode int) {
//	w.Header().Set("Content-Type", "application/json")
//	w.WriteHeader(statusCode)
//	fmt.Fprint(w, err.Error())
//}

func handleError(c *gin.Context, err error) {
	c.JSON(http.StatusOK, gin.H{
		"code": http.StatusInternalServerError,
		"msg":  err.Error(),
	})
}

//func handleSuccess(w http.ResponseWriter, response interface{}) {
//	w.Header().Set("Content-Type", "application/json")
//	w.WriteHeader(http.StatusOK)
//	err := json.NewEncoder(w).Encode(response)
//	if err != nil {
//		handleError(w, err, http.StatusInternalServerError)
//		return
//	}
//}

func handleSuccess(c *gin.Context, response interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code": http.StatusOK,
		"data": response,
	})
}

func main() {

	internal.Init()

	r := gin.Default()
	r.Use(util.RequestResponseLogger())
	r.POST("/fetch_rank", func(c *gin.Context) {
		request := &dto.FetchRankListRequest{}
		if err := c.ShouldBindJSON(request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp, err := processor.FetchRankList(c, request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	r.POST("/upload_score", func(c *gin.Context) {
		request := &dto.UploadScoreRequest{}
		if err := c.ShouldBindJSON(request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp, err := processor.UploadScore(c, request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	r.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")

	// Register the handler for /fetch_rank endpoint
	//http.HandleFunc("/fetch_rank", fetchRankHandler)
	//http.HandleFunc("/upload_score", uploadScoreHandler)
	//// Start the server
	//fmt.Println("Server starting on port 8080...")
	//
	//log.Fatal(http.ListenAndServe(":8080", nil))
}
