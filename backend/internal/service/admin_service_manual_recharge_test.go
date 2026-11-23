//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/stretchr/testify/require"
)

func newAdminManualRechargeService(t *testing.T, user *User) (*adminServiceImpl, *balanceUserRepoStub, *balanceRedeemRepoStub) {
	t.Helper()

	client := newPaymentConfigServiceTestClient(t)
	// 订单对 users 表有外键，先落一行真实用户，并把自增 ID 回填给被测用户
	created, err := client.User.Create().
		SetEmail(user.Email).
		SetUsername(user.Username).
		SetPasswordHash("x").
		SetNotes(user.Notes).
		Save(context.Background())
	require.NoError(t, err)
	user.ID = created.ID

	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: user}}
	redeemRepo := &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}}
	svc := &adminServiceImpl{
		userRepo:       repo,
		redeemCodeRepo: redeemRepo,
		entClient:      client,
	}
	return svc, repo, redeemRepo
}

// 扫码人工充值没有用户侧下单流程：带上 pay_amount 的管理员手动充值
// 必须补落一笔已支付订单，用户才能在「我的订单」中看到这笔充值。
func TestAdminService_UpdateUserBalance_CreatesManualRechargeOrder(t *testing.T) {
	user := &User{ID: 7, Balance: 10, Email: "u@example.com", Username: "alice", Notes: "vip"}
	svc, _, redeemRepo := newAdminManualRechargeService(t, user)

	payAmount := 12.5
	updated, err := svc.UpdateUserBalance(context.Background(), 7, 15, "add", "扫码人工充值备注", &payAmount)
	require.NoError(t, err)
	require.Equal(t, 25.0, updated.Balance)

	orders, err := svc.entClient.PaymentOrder.Query().
		Where(paymentorder.SrcHostEQ(manualRechargeSrcHost)).
		All(context.Background())
	require.NoError(t, err)
	require.Len(t, orders, 1)

	order := orders[0]
	require.Equal(t, updated.ID, order.UserID)
	require.Equal(t, "u@example.com", order.UserEmail)
	require.Equal(t, "alice", order.UserName)
	require.NotNil(t, order.UserNotes)
	require.Equal(t, "vip", *order.UserNotes)
	require.Equal(t, 15.0, order.Amount)     // 到账额度
	require.Equal(t, 12.5, order.PayAmount)  // 实付金额
	require.Equal(t, 0.2, order.FeeRate)     // (15 - 12.5) / 12.5
	require.Equal(t, "alipay", order.PaymentType)
	require.Equal(t, "balance", order.OrderType)
	require.Equal(t, OrderStatusCompleted, order.Status)
	require.Equal(t, "", order.RechargeCode)
	require.Equal(t, "", order.PaymentTradeNo)
	require.Equal(t, "127.0.0.1", order.ClientIP)
	require.NotNil(t, order.RefundReason)
	require.Equal(t, "扫码人工充值备注", *order.RefundReason)
	require.NotEmpty(t, order.OutTradeNo)
	require.Contains(t, order.OutTradeNo, "MR")
	require.NotNil(t, order.PaidAt)
	// 已支付订单，超时时间不再有意义，仅作字段占位
	require.True(t, order.ExpiresAt.After(order.CreatedAt))

	// 同时仍要写入余额调整记录
	require.Len(t, redeemRepo.created, 1)
	require.Equal(t, AdjustmentTypeAdminBalance, redeemRepo.created[0].Type)
	require.Equal(t, 15.0, redeemRepo.created[0].Value)
}

// 不带 pay_amount 的普通调账不应该产生任何订单。
func TestAdminService_UpdateUserBalance_NoOrderWithoutPayAmount(t *testing.T) {
	user := &User{ID: 7, Balance: 10, Email: "u@example.com", Username: "alice"}
	svc, _, _ := newAdminManualRechargeService(t, user)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "", nil)
	require.NoError(t, err)

	count, err := svc.entClient.PaymentOrder.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)

	// 非 add 操作同样不落单
	payAmount := 12.5
	_, err = svc.UpdateUserBalance(context.Background(), 7, 8, "set", "", &payAmount)
	require.NoError(t, err)

	count, err = svc.entClient.PaymentOrder.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}
