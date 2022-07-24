package zorm

import "context"

// IGlobalTransaction 分布式事务的包装接口,隔离seata/hptx等的依赖
// 声明一个struct,实现这个接口,并配置实现 FuncGlobalTransaction 函数
/**

//不使用proxy代理模式,全局托管,不修改业务代码,零侵入实现分布式事务
//tm.Implement(svc.ProxySvc)


// 分布式事务示例代码
_, err := zorm.Transaction(ctx, func(ctx context.Context) (interface{}, error) {

    // 获取当前分布式事务的XID.不用考虑怎么来的,如果是分布式事务环境,会自动设置值
    // xid := ctx.Value("XID").(string)

	// 把xid传递到第三方应用
	// req.Header.Set("XID", xid)

	// 如果返回的err不是nil,本地事务和分布式事务就会回滚
	return nil, err
})

///----------第三方应用-------///

// 第三方应用开启事务前,ctx需要绑定XID,例如使用了gin框架

// 接受传递过来的XID,绑定到本地ctx
// xid:=c.Request.Header.Get("XID")
// 获取到ctx
// ctx := c.Request.Context()
// ctx = context.WithValue(ctx,"XID",xid)

// ctx绑定XID之后,调用业务事务
_, err := zorm.Transaction(ctx, func(ctx context.Context) (interface{}, error) {

    // 业务代码......

	// 如果返回的err不是nil,本地事务和分布式事务就会回滚
	return nil, err
})


// 建议以下代码放到单独的文件里
//................//

// ZormGlobalTransaction 包装seata/hptx的*tm.DefaultGlobalTransaction,实现zorm.IGlobalTransaction接口
type ZormGlobalTransaction struct {
	*tm.DefaultGlobalTransaction
}

// MyFuncGlobalTransaction zorm适配seata/hptx 全局分布式事务的函数
// 重要!!!!需要配置zorm.DataSourceConfig.FuncGlobalTransaction=MyFuncGlobalTransaction 重要!!!
func MyFuncGlobalTransaction(ctx context.Context) (zorm.IGlobalTransaction, context.Context, error) {
	//获取seata/hptx的rootContext
	rootContext := gtxContext.NewRootContext(ctx)
	//创建seata/hptx事务
	globalTx := tm.GetCurrentOrCreate(rootContext)
	//使用zorm.IGlobalTransaction接口对象包装分布式事务,隔离seata/hptx依赖
	globalTransaction := &ZormGlobalTransaction{globalTx}

	return globalTransaction, rootContext, nil
}


//实现zorm.IGlobalTransaction 托管全局分布式事务接口,seata和hptx目前实现代码一致,只是引用的实现包不同
// Begin 开启全局分布式事务
func (gtx *ZormGlobalTransaction) Begin(ctx context.Context) error {
	rootContext := ctx.(*gtxContext.RootContext)
	return gtx.BeginWithTimeout(int32(6000), rootContext)
}

// Commit 提交全局分布式事务
func (gtx *ZormGlobalTransaction) Commit(ctx context.Context) error {
	rootContext := ctx.(*gtxContext.RootContext)
	return gtx.Commit(rootContext)
}

// Rollback 回滚全局分布式事务
func (gtx *ZormGlobalTransaction) Rollback(ctx context.Context) error {
	rootContext := ctx.(*gtxContext.RootContext)
	//如果是Participant角色,修改为Launcher角色,允许分支事务提交全局事务.
	if gtx.Role != tm.Launcher {
		gtx.Role = tm.Launcher
	}
	return gtx.Rollback(rootContext)
}
// GetXID 获取全局分布式事务的XID
func (gtx *ZormGlobalTransaction) GetXID(ctx context.Context) string {
	rootContext := ctx.(*gtxContext.RootContext)
	return rootContext.GetXID()
}
//................//
**/

// IGlobalTransaction 托管全局分布式事务接口,seata和hptx目前实现代码一致,只是引用的实现包不同
type IGlobalTransaction interface {
	// Begin 开启全局分布式事务
	Begin(ctx context.Context) error

	// Commit 提交全局分布式事务
	Commit(ctx context.Context) error

	// Rollback 回滚全局分布式事务
	Rollback(ctx context.Context) error

	// GetXID 获取全局分布式事务的XID
	GetXID(ctx context.Context) string

	//重新包装为 seata/hptx 的context.RootContext
	//context.RootContext 如果后续使用了 context.WithValue,类型就是context.valueCtx 就会造成无法再类型断言为 context.RootContext
	//所以DBDao里使用了 globalRootContext变量,区分业务的ctx和分布式事务的RootContext
	//NewRootContext(ctx context.Context) context.Context
}