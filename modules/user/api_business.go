package user

import (
	"context"
	"errors"

	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/config"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/util"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/wkhttp"
	"github.com/opentracing/opentracing-go"
	"go.uber.org/zap"
)

const (
	BusinessAuthHeaderKey = "X-Business-Token" // 业务系统认证header key
)

// 业务系统用户登录
func (u *User) businessLogin(c *wkhttp.Context) {
	// 验证业务系统token
	businessToken := c.GetHeader(BusinessAuthHeaderKey)
	if businessToken == "" {
		c.ResponseError(errors.New("缺少业务系统认证信息"))
		return
	}
	if !u.verifyBusinessToken(businessToken) {
		c.ResponseError(errors.New("业务系统认证失败"))
		return
	}

	var req businessLoginReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("请求数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	loginSpan := u.ctx.Tracer().StartSpan(
		"business_login",
		opentracing.ChildOf(c.GetSpanContext()),
	)
	loginSpanCtx := u.ctx.Tracer().ContextWithSpan(context.Background(), loginSpan)
	defer loginSpan.Finish()

	var userInfo *Model
	var err error

	if req.UID != "" {
		// 如果提供了UID，查询用户是否存在
		userInfo, err = u.db.QueryByUID(req.UID)
		if err != nil {
			u.Error("查询用户信息失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	var result *loginUserDetailResp
	if userInfo != nil {
		// 用户存在，直接登录
		result, err = u.execLogin(userInfo, config.DeviceFlag(req.Flag), req.Device, loginSpanCtx)
		if err != nil {
			c.ResponseError(err)
			return
		}
	} else {
		// 用户不存在或未提供UID，创建新用户
		uid := util.GenerUUID()
		var model = &createUserModel{
			UID:      uid,
			Name:     req.Name,
			Sex:      req.Sex,
			Password: "", // 不需要密码
			Flag:     int(req.Flag),
			Device:   req.Device,
			Zone:     req.Zone,
			Phone:    req.Phone,
			Username: req.Username,
		}

		tx, err := u.db.session.Begin()
		if err != nil {
			u.Error("创建事务失败！", zap.Error(err))
			c.ResponseError(errors.New("创建事务失败！"))
			return
		}
		defer func() {
			if err := recover(); err != nil {
				tx.Rollback()
				panic(err)
			}
		}()
		publicIP := util.GetClientPublicIP(c.Request)
		result, err = u.createUserWithRespAndTx(loginSpanCtx, model, publicIP, nil, tx, func() error {
			err := tx.Commit()
			if err != nil {
				tx.Rollback()
				u.Error("数据库事务提交失败", zap.Error(err))
				return errors.New("数据库事务提交失败")
			}
			return nil
		})
		if err != nil {
			tx.Rollback()
			c.ResponseError(errors.New("注册失败！"))
			return
		}
	}

	c.Response(result)

	// 发送欢迎消息
	publicIP := util.GetClientPublicIP(c.Request)
	go u.sentWelcomeMsg(publicIP, result.UID)
}

// 验证业务系统token
func (u *User) verifyBusinessToken(token string) bool {
	// 从配置中获取业务系统token列表
	businessTokens := u.ctx.GetConfig().Business.Tokens
	if len(businessTokens) == 0 {
		return false
	}
	// 验证token是否在允许的列表中
	for _, t := range businessTokens {
		if t == token {
			return true
		}
	}
	return false
}

type businessLoginReq struct {
	UID      string     `json:"uid"`      // 业务系统用户ID，可选
	Name     string     `json:"name"`     // 用户昵称
	Sex      int        `json:"sex"`      // 性别 1:男 2:女
	Flag     uint8      `json:"flag"`     // 设备标记 0.APP 1.PC
	Device   *deviceReq `json:"device"`   // 设备信息
	Zone     string     `json:"zone"`     // 区号
	Phone    string     `json:"phone"`    // 手机号
	Username string     `json:"username"` // 用户名
}

func (r *businessLoginReq) Check() error {
	if r.Name == "" {
		return errors.New("用户昵称不能为空！")
	}
	if r.Sex != 1 && r.Sex != 2 {
		return errors.New("性别参数错误！")
	}
	return nil
}
