// Copyright 2017 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package swagger

import (
	api "code.gitea.io/gitea/modules/structs"
)

// Issue
// swagger:response Issue
type swaggerResponseIssue struct {
	// in:body
	Body api.Issue `json:"body"`
}

// IssueList
// swagger:response IssueList
type swaggerResponseIssueList struct {
	// in:body
	Body []api.Issue `json:"body"`
}

// Comment
// swagger:response Comment
type swaggerResponseComment struct {
	// in:body
	Body api.Comment `json:"body"`
}

// CommentList
// swagger:response CommentList
type swaggerResponseCommentList struct {
	// in:body
	Body []api.Comment `json:"body"`
}

// Label
// swagger:response Label
type swaggerResponseLabel struct {
	// in:body
	Body api.Label `json:"body"`
}

// LabelList
// swagger:response LabelList
type swaggerResponseLabelList struct {
	// in:body
	Body []api.Label `json:"body"`
}

// Milestone
// swagger:response Milestone
type swaggerResponseMilestone struct {
	// in:body
	Body api.Milestone `json:"body"`
}

// MilestoneList
// swagger:response MilestoneList
type swaggerResponseMilestoneList struct {
	// in:body
	Body []api.Milestone `json:"body"`
}

// TrackedTime
// swagger:response TrackedTime
type swaggerResponseTrackedTime struct {
	// in:body
	Body api.TrackedTime `json:"body"`
}

// TrackedTimeList
// swagger:response TrackedTimeList
type swaggerResponseTrackedTimeList struct {
	// in:body
	Body []api.TrackedTime `json:"body"`
}

// IssueDeadline
// swagger:response IssueDeadline
type swaggerIssueDeadline struct {
	// in:body
	Body api.IssueDeadline `json:"body"`
}

// StopWatch
// swagger:response StopWatch
type swaggerResponseStopWatch struct {
	// in:body
	Body api.StopWatch `json:"body"`
}

// StopWatchList
// swagger:response StopWatchList
type swaggerResponseStopWatchList struct {
	// in:body
	Body []api.StopWatch `json:"body"`
}

// EditReactionOption
// swagger:response EditReactionOption
type swaggerEditReactionOption struct {
	// in:body
	Body api.EditReactionOption `json:"body"`
}

// ReactionResponse
// swagger:response ReactionResponse
type swaggerReactionResponse struct {
	// in:body
	Body api.ReactionResponse `json:"body"`
}

// ReactionResponseList
// swagger:response ReactionResponseList
type swaggerReactionResponseList struct {
	// in:body
	Body []api.ReactionResponse `json:"body"`
}
