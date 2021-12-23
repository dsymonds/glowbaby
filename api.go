package main

// LoginResponse represents the JSON response from an /android/user/sign_in request.
type LoginResponse struct {
	Data struct {
		Babies []struct {
			Baby struct {
				BabyID int64 `json:"baby_id"`

				FirstName string `json:"first_name"`
				LastName  string `json:"last_name"`
				Birthday  string `json:"birthday"` // "YYYY/MM/DD" format
			} `json:"Baby"`
		} `json:"babies"`

		User struct {
			AuthToken string `json:"encrypted_token"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"user"`
	} `json:"data"`
}

// PullResponse represents the JSON response from an /android/user/pull fetch.
type PullResponse struct {
	Data struct {
		Babies []struct {
			BabyID    int64  `json:"baby_id"`
			SyncTime  int64  `json:"sync_time"`
			SyncToken string `json:"sync_token"`

			BabyData struct {
				Remove []BabyData `json:"remove"`
				Update []BabyData `json:"update"`
			} `json:"BabyData"`

			BabyFeedData struct {
				Remove []BabyFeedData `json:"remove"`
				Update []BabyFeedData `json:"update"`
			} `json:"BabyFeedData"`

			// Other keys:
			//   "Baby" (static info about baby)
			//   "BabyFamily" (parent info)
			//   "BabyMilestone"
			//   "MilestonePhoto"
			//   "Photo"
			//   "UserBabyRelation"
		} `json:"babies"`

		// Other keys: "insights", "syncable_insights", "user"
	} `json:"data"`

	// Other keys: "rc" (response code? 0 on success)
}

type BabyData struct {
	ID     int64 `json:"id"`
	BabyID int64 `json:"baby_id"`

	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   *int64 `json:"end_timestamp"`

	Key string `json:"key"` // e.g. "medicine", "sleep", "tummy"

	// For key=diaper, val_int may have these values:
	//	65536
	//	66625
	//	1089
	//	1041
	//	17
	ValInt int64 `json:"val_int"`

	// Used for key=temperature (ºC), or key=weight (kg), or key=height (cm)
	ValFloat float32 `json:"val_float"`

	// Used for key=medicine
	ValStr string `json:"val_str"`

	// "uuid"
}

type BabyFeedData struct {
	ID     int64 `json:"id"`
	BabyID int64 `json:"baby_id"`

	StartTimestamp int64 `json:"start_timestamp"`

	FeedType int64 `json:"feed_type"` // e.g. 1

	BreastUsed  string `json:"breast_used"`       // e.g. "R"
	BreastLeft  int64  `json:"breast_left_time"`  // seconds
	BreastRight int64  `json:"breast_right_time"` // seconds

	BottleML float64 `json:"bottle_ml"`

	// "uuid"
}
