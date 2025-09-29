package gorm

import (
	"context"
	"hostlink/domain/nonce"
	"time"

	"gorm.io/gorm"
)

type NonceRepository struct {
	db *gorm.DB
}

func NewNonceRepository(db *gorm.DB) *NonceRepository {
	return &NonceRepository{db: db}
}

func (r *NonceRepository) Save(ctx context.Context, n *nonce.Nonce) error {
	return r.db.WithContext(ctx).Create(n).Error
}

func (r *NonceRepository) FindByValue(ctx context.Context, value string) (*nonce.Nonce, error) {
	var n nonce.Nonce
	err := r.db.WithContext(ctx).Where("value = ?", value).First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *NonceRepository) Exists(ctx context.Context, value string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&nonce.Nonce{}).Where("value = ?", value).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *NonceRepository) DeleteExpired(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-olderThan)
	result := r.db.WithContext(ctx).Where("created_at < ?", cutoffTime).Delete(&nonce.Nonce{})
	return result.RowsAffected, result.Error
}

func (r *NonceRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	cutoffTime := time.Now().Add(-5 * time.Minute)
	err := r.db.WithContext(ctx).Model(&nonce.Nonce{}).Where("created_at >= ?", cutoffTime).Count(&count).Error
	return count, err
}

func (r *NonceRepository) Transaction(ctx context.Context, fn func(*NonceRepository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		txRepo := &NonceRepository{db: tx}
		return fn(txRepo)
	})
}