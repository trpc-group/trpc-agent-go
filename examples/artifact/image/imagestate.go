package main

const generateImageStateKey = "generateImageStateKey"

type generateImageStateValue struct {
	ImageIDs []string `json:"image_ids"`
}
