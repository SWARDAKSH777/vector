package main

import "context"

func withUserID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxUserID, id)
}

func userIDFromCtx(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxUserID).(int64)
	return v
}
