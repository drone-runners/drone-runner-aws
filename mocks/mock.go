package mocks

//go:generate mockgen -source=../store/store.go -destination=store_mock.go -package=mocks
//go:generate mockgen -source=../internal/drivers/pool.go -destination=pool_mock.go -package=mocks
//go:generate mockgen -source=../internal/le/client.go -destination=lite_client.go -package=mocks
