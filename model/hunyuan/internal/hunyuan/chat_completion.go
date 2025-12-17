//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hunyuan

// ChatCompletionNewParams https://cloud.tencent.com/document/api/1729/105701#2.-.E8.BE.93.E5.85.A5.E5.8F.82.E6.95.B0
type ChatCompletionNewParams struct {
	Model                      string                          `json:"Model"`
	Version                    string                          `json:"Version,omitempty"`
	Messages                   []*ChatCompletionMessageParam   `json:"Messages"`
	Stream                     bool                            `json:"Stream,omitempty"`
	StreamModeration           bool                            `json:"StreamModeration,omitempty"`
	TopP                       float64                         `json:"TopP,omitempty"`
	Temperature                float64                         `json:"Temperature,omitempty"`
	EnableEnhancement          bool                            `json:"EnableEnhancement,omitempty"`
	Tools                      []*ChatCompletionMessageTool    `json:"Tools,omitempty"`
	ToolChoice                 string                          `json:"ToolChoice,omitempty"`
	CustomTool                 *ChatCompletionMessageTool      `json:"CustomTool,omitempty"`
	SearchInfo                 bool                            `json:"SearchInfo,omitempty"`
	Citation                   bool                            `json:"Citation,omitempty"`
	EnableSpeedSearch          bool                            `json:"EnableSpeedSearch,omitempty"`
	EnableMultimedia           bool                            `json:"EnableMultimedia,omitempty"`
	EnableDeepSearch           bool                            `json:"EnableDeepSearch,omitempty"`
	Seed                       int                             `json:"Seed,omitempty"` // 0-10000
	ForceSearchEnhancement     bool                            `json:"ForceSearchEnhancement,omitempty"`
	Stop                       []string                        `json:"Stop,omitempty"`
	EnableRecommendedQuestions bool                            `json:"EnableRecommendedQuestions,omitempty"`
	EnableDeepRead             bool                            `json:"EnableDeepRead,omitempty"`
	WebSearchOptions           *ChatCompletionWebSearchOptions `json:"WebSearchOptions,omitempty"`
	TopicChoice                string                          `json:"TopicChoice,omitempty"`
	EnableThinking             bool                            `json:"EnableThinking,omitempty"`
}

// ChatCompletionMessageParam https://cloud.tencent.com/document/api/1729/101838#Message
type ChatCompletionMessageParam struct {
	Role             string                               `json:"Role"` // system, user, assistant, tool
	Content          string                               `json:"Content,omitempty"`
	Contents         []*ChatCompletionMessageContentParam `json:"Contents,omitempty"`
	ToolCallId       string                               `json:"ToolCallId,omitempty"`
	ToolCalls        []*ChatCompletionMessageToolCall     `json:"ToolCalls,omitempty"`
	FileIDs          []string                             `json:"FileIds,omitempty"`
	ReasoningContent string                               `json:"ReasoningContent,omitempty"`
}

// ChatCompletionMessageContentParam https://cloud.tencent.com/document/api/1729/101838#Content
type ChatCompletionMessageContentParam struct {
	Type        string                                `json:"Type"`
	Text        string                                `json:"Text,omitempty"`
	ImageUrl    *ChatCompletionContentImageUrlParam   `json:"ImageUrl,omitempty"`
	VideoUrl    *ChatCompletionContentVideoUrlParam   `json:"VideoUrl,omitempty"`
	VideoFrames *ChatCompletionContentVideoFrameParam `json:"VideoFrames,omitempty"`
}

// ChatCompletionContentImageUrlParam https://cloud.tencent.com/document/api/1729/101838#ImageUrl
type ChatCompletionContentImageUrlParam struct {
	Url string `json:"Url,omitempty"`
}

// ChatCompletionContentVideoUrlParam https://cloud.tencent.com/document/api/1729/101838#VideoUrl
type ChatCompletionContentVideoUrlParam struct {
	Url string  `json:"Url,omitempty"`
	Fps float64 `json:"Fps,omitempty"`
}

// ChatCompletionContentVideoFrameParam https://cloud.tencent.com/document/api/1729/101838#VideoFrames
type ChatCompletionContentVideoFrameParam struct {
	Frames []string `json:"Frames,omitempty"`
}

// ChatCompletionMessageTool https://cloud.tencent.com/document/api/1729/101838#Tool
type ChatCompletionMessageTool struct {
	Type     string                             `json:"Type"`
	Function *ChatCompletionMessageToolFunction `json:"Function"`
}

// ChatCompletionMessageToolCall https://cloud.tencent.com/document/api/1729/101838#ToolCall
type ChatCompletionMessageToolCall struct {
	Id       string                                 `json:"Id"`
	Type     string                                 `json:"Type"`
	Function *ChatCompletionMessageToolCallFunction `json:"Function"`
	Index    int                                    `json:"Index"`
}

// ChatCompletionMessageToolFunction https://cloud.tencent.com/document/api/1729/101838#ToolFunction
type ChatCompletionMessageToolFunction struct {
	Name        string `json:"Name"`
	Parameters  string `json:"Parameters"`
	Description string `json:"Description,omitempty"`
}

// ChatCompletionMessageToolCallFunction https://cloud.tencent.com/document/api/1729/101838#ToolCallFunction
type ChatCompletionMessageToolCallFunction struct {
	Name      string `json:"Name"`
	Arguments string `json:"Arguments"` // json string
}

// ChatCompletionWebSearchOptions https://cloud.tencent.com/document/api/1729/101838#WebSearchOptions
type ChatCompletionWebSearchOptions struct {
	Knowledge []*ChatCompletionWebSearchKnowledge `json:"Knowledge,omitempty"`
}

// ChatCompletionWebSearchKnowledge https://cloud.tencent.com/document/api/1729/101838#Knowledge
type ChatCompletionWebSearchKnowledge struct {
	Text string `json:"Text"`
}

// ChatCompletionWebSearchUserLocation https://cloud.tencent.com/document/api/1729/101838#UserLocation
type ChatCompletionWebSearchUserLocation struct {
	Type        string                              `json:"Type,omitempty"`
	Approximate *ChatCompletionWebSearchApproximate `json:"Approximate,omitempty"`
}

// ChatCompletionWebSearchApproximate https://cloud.tencent.com/document/api/1729/101838#Approximate
type ChatCompletionWebSearchApproximate struct {
	Country  string `json:"Country,omitempty"`
	City     string `json:"City,omitempty"`
	Region   string `json:"Region,omitempty"`
	Timezone string `json:"Timezone,omitempty"`
	Address  string `json:"Address,omitempty"`
}

// ChatCompletionResponse https://cloud.tencent.com/document/api/1729/105701#3.-.E8.BE.93.E5.87.BA.E5.8F.82.E6.95.B0
type ChatCompletionResponse struct {
	Created              int64                             `json:"Created"`
	Usage                ChatCompletionResponseUsage       `json:"Usage"`
	Note                 string                            `json:"Note"`
	Id                   string                            `json:"Id"`
	Choices              []*ChatCompletionResponseChoice   `json:"Choices"`
	ErrorMsg             *ChatCompletionResponseErrMsg     `json:"ErrorMsg"`
	SearchInfo           *ChatCompletionResponseSearchInfo `json:"SearchInfo,omitempty"`
	Replaces             []*ChatCompletionResponseReplace  `json:"Replaces,omitempty"`
	RecommendedQuestions []string                          `json:"RecommendedQuestions,omitempty"`
	Processes            []*ChatCompletionResponseProcess  `json:"Processes,omitempty"`
	RequestId            string                            `json:"RequestId"`
	Error                *ChatCompletionErrorInfo          `json:"Error,omitempty"`
}

// ChatCompletionErrorInfo ...
type ChatCompletionErrorInfo struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

// chatCompletionResponseData ...
type chatCompletionResponseData struct {
	Response ChatCompletionResponse `json:"Response"`
}

// ChatCompletionResponseUsage https://cloud.tencent.com/document/api/1729/101838#Usage
type ChatCompletionResponseUsage struct {
	PromptTokens     int64 `json:"PromptTokens"`
	CompletionTokens int64 `json:"CompletionTokens"`
	TotalTokens      int64 `json:"TotalTokens"`
}

// ChatCompletionResponseChoice https://cloud.tencent.com/document/api/1729/101838#Choice
type ChatCompletionResponseChoice struct {
	FinishReason    string                       `json:"FinishReason"`
	Delta           *ChatCompletionResponseDelta `json:"Delta,omitempty"`
	Message         *ChatCompletionMessageParam  `json:"Message,omitempty"`
	Index           int                          `json:"Index"`
	ModerationLevel string                       `json:"ModerationLevel"`
}

// ChatCompletionResponseDelta https://cloud.tencent.com/document/api/1729/101838#Delta
type ChatCompletionResponseDelta struct {
	Role             string                           `json:"Role"`
	Content          string                           `json:"Content"`
	ToolCalls        []*ChatCompletionMessageToolCall `json:"ToolCalls"`
	ReasoningContent string                           `json:"ReasoningContent"`
}

// ChatCompletionResponseErrMsg https://cloud.tencent.com/document/api/1729/101838#ErrorMsg
type ChatCompletionResponseErrMsg struct {
	Msg  string `json:"Msg"`
	Code int    `json:"Code"`
}

// ChatCompletionResponseSearchInfo https://cloud.tencent.com/document/api/1729/101838#SearchInfo
type ChatCompletionResponseSearchInfo struct {
	SearchResults     []*ChatCompletionResponseSearchInfoResult `json:"SearchResults,omitempty"`
	Mindmap           *ChatCompletionResponseMindmap            `json:"Mindmap,omitempty"`
	RelevantEvents    []*ChatCompletionResponseRelevantEvent    `json:"RelevantEvents,omitempty"`
	RelevantEntities  []*ChatCompletionResponseRelevantEntity   `json:"RelevantEntities,omitempty"`
	Timeline          []*ChatCompletionResponseTimeline         `json:"Timeline,omitempty"`
	SupportDeepSearch bool                                      `json:"SupportDeepSearch,omitempty"`
	Outline           []string                                  `json:"Outline,omitempty"`
}

// ChatCompletionResponseSearchInfoResult https://cloud.tencent.com/document/api/1729/101838#SearchResult
type ChatCompletionResponseSearchInfoResult struct {
	Index int    `json:"Index,omitempty"`
	Title string `json:"Title,omitempty"`
	Url   string `json:"Url,omitempty"`
	Text  string `json:"Text"`
	Icon  string `json:"Icon"`
}

// ChatCompletionResponseMindmap https://cloud.tencent.com/document/api/1729/101838#Mindmap
type ChatCompletionResponseMindmap struct {
	ThumbUrl string `json:"ThumbUrl,omitempty"`
	Url      string `json:"Url,omitempty"`
}

// ChatCompletionResponseRelevantEvent https://cloud.tencent.com/document/api/1729/101838#RelevantEvent
type ChatCompletionResponseRelevantEvent struct {
	Title     string `json:"Title,omitempty"`
	Content   string `json:"Content,omitempty"`
	Datetime  string `json:"Datetime,omitempty"`
	Reference []int  `json:"Reference,omitempty"`
}

// ChatCompletionResponseRelevantEntity https://cloud.tencent.com/document/api/1729/101838#RelevantEntity
type ChatCompletionResponseRelevantEntity struct {
	Name      string `json:"Name,omitempty"`
	Content   string `json:"Content,omitempty"`
	Reference []int  `json:"Reference,omitempty"`
}

// ChatCompletionResponseTimeline https://cloud.tencent.com/document/api/1729/101838#Timeline
type ChatCompletionResponseTimeline struct {
	Title    string `json:"Title,omitempty"`
	Datetime string `json:"Datetime,omitempty"`
	Url      string `json:"Url,omitempty"`
}

// ChatCompletionResponseReplace https://cloud.tencent.com/document/api/1729/101838#Replace
type ChatCompletionResponseReplace struct {
	Id         string                              `json:"Id,omitempty"`
	Multimedia []*ChatCompletionResponseMultimedia `json:"Multimedia,omitempty"`
}

// ChatCompletionResponseMultimedia https://cloud.tencent.com/document/api/1729/101838#Multimedia
type ChatCompletionResponseMultimedia struct {
	Type        string                         `json:"Type,omitempty"`
	Url         string                         `json:"Url,omitempty"`
	Width       int                            `json:"Width,omitempty"`
	Height      int                            `json:"Height,omitempty"`
	JumpUrl     string                         `json:"JumpUrl,omitempty"`
	ThumbURL    string                         `json:"ThumbURL,omitempty"`
	ThumbWidth  int                            `json:"ThumbWidth,omitempty"`
	ThumbHeight int                            `json:"ThumbHeight,omitempty"`
	Title       string                         `json:"Title,omitempty"`
	Desc        string                         `json:"Desc,omitempty"`
	Singer      string                         `json:"Singer,omitempty"`
	Ext         *ChatCompletionResponseSongExt `json:"Ext,omitempty"`
	PublishTime string                         `json:"PublishTime,omitempty"`
	SiteName    string                         `json:"SiteName,omitempty"`
	SiteIcon    string                         `json:"SiteIcon,omitempty"`
}

// ChatCompletionResponseSongExt https://cloud.tencent.com/document/api/1729/101838#SongExt
type ChatCompletionResponseSongExt struct {
	SongId  int    `json:"SongId,omitempty"`
	SongMid string `json:"SongMid,omitempty"`
	Vip     int    `json:"Vip,omitempty"`
}

// ChatCompletionResponseProcess https://cloud.tencent.com/document/api/1729/101838#Processes
type ChatCompletionResponseProcess struct {
	Message string `json:"Message,omitempty"`
	State   string `json:"State,omitempty"`
	Num     int    `json:"Num,omitempty"`
}
