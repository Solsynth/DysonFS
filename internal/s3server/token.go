package s3server

import "context"

type contextKey string

const tokenInfoContextKey contextKey = "s3token"

type TokenInfo struct {
	AccountID string
	PoolID    *string
}

func TokenInfoFromContext(ctx context.Context) *TokenInfo {
	v, _ := ctx.Value(tokenInfoContextKey).(*TokenInfo)
	return v
}

type TokenResolver interface {
	ResolveS3Credentials(ctx context.Context, accessKey string) (secretKey string, info *TokenInfo, err error)
}
