package eventbus

type FileUploadedEvent struct {
	FileID             string `json:"file_id"`
	TaskID             string `json:"task_id"`
	RemoteID           string `json:"remote_id"`
	StorageID          string `json:"storage_id,omitempty"`
	ContentType        string `json:"content_type,omitempty"`
	ProcessingFilePath string `json:"processing_file_path"`
	IsTempFile         bool   `json:"is_temp_file"`
}

type FileActionEvent struct {
	Action   string `json:"action"`
	FileID   string `json:"file_id"`
	AccountID string `json:"account_id"`
	Name     string `json:"name,omitempty"`
}
