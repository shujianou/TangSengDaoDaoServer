package user

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"hash/crc32"

	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/common"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/config"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/util"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/wkhttp"
	"github.com/gocraft/dbr/v2"
	"github.com/opentracing/opentracing-go"
	"go.uber.org/zap"
)

const (
	BusinessAuthHeaderKey = "WKIM-Business-Token" // 业务系统认证header key
)

// 业务系统错误定义
var (
	ErrBusinessTokenMissing = errors.New("缺少业务系统认证信息")
	ErrBusinessTokenInvalid = errors.New("业务系统认证失败")
	ErrInvalidRequestData   = errors.New("请求数据格式有误")
	ErrUserNotFound         = errors.New("用户不存在")
	ErrDBTransaction        = errors.New("数据库事务操作失败")
)

// 业务系统用户登录
func (u *User) businessLogin(c *wkhttp.Context) {
	// 验证业务系统token
	if err := u.verifyBusinessTokenFromHeader(c); err != nil {
		c.ResponseError(err)
		return
	}

	var req businessLoginReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(ErrInvalidRequestData)
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

	result, err := u.handleBusinessLogin(loginSpanCtx, &req, c)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.Response(result)

	// 处理头像上传
	if req.Avatar != "" && result != nil {
		go u.handleAvatarUpload(req.Avatar, result.UID)
	}

	// 发送欢迎消息
	publicIP := util.GetClientPublicIP(c.Request)
	go u.sentWelcomeMsg(publicIP, result.UID)
}

// 处理业务系统登录逻辑
func (u *User) handleBusinessLogin(ctx context.Context, req *businessLoginReq, c *wkhttp.Context) (*loginUserDetailResp, error) {
	var userInfo *Model
	var err error

	if req.UID != "" {
		userInfo, err = u.db.QueryByUID(req.UID)
		if err != nil {
			u.Error("查询用户信息失败", zap.Error(err))
			return nil, err
		}
	}

	if userInfo != nil {
		// 用户存在，直接登录
		return u.execLogin(userInfo, config.DeviceFlag(req.Flag), req.Device, ctx)
	}

	// 用户不存在，创建新用户
	return u.createNewBusinessUser(ctx, req, c)
}

// 创建新的业务系统用户
func (u *User) createNewBusinessUser(ctx context.Context, req *businessLoginReq, c *wkhttp.Context) (*loginUserDetailResp, error) {
	uid := util.GenerUUID()
	model := &createUserModel{
		UID:      uid,
		Name:     req.Name,
		Sex:      req.Sex,
		Password: "", // 业务系统用户不需要密码
		Flag:     int(req.Flag),
		Device:   req.Device,
		Zone:     req.Zone,
		Phone:    req.Phone,
		Username: req.Username,
	}

	tx, err := u.db.session.Begin()
	if err != nil {
		u.Error("创建事务失败", zap.Error(err))
		return nil, ErrDBTransaction
	}
	defer tx.Rollback()

	publicIP := util.GetClientPublicIP(c.Request)
	result, err := u.createUserWithRespAndTx(ctx, model, publicIP, nil, tx, func() error {
		if err := tx.Commit(); err != nil {
			u.Error("数据库事务提交失败", zap.Error(err))
			return ErrDBTransaction
		}
		return nil
	})
	if err != nil {
		u.Error("创建用户失败", zap.Error(err))
		return nil, errors.New("注册失败")
	}

	return result, nil
}

// 处理头像上传
func (u *User) handleAvatarUpload(avatarURL string, uid string) {
	// 下载头像
	reader, err := u.fileService.DownloadImage(avatarURL, context.Background())
	if err != nil {
		u.Error("下载头像失败", zap.Error(err))
		return
	}
	defer reader.Close()

	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "avatar-*.png")
	if err != nil {
		u.Error("创建临时文件失败", zap.Error(err))
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// 将下载的数据写入临时文件
	if _, err = io.Copy(tmpFile, reader); err != nil {
		u.Error("保存头像数据失败", zap.Error(err))
		return
	}

	// 重置文件指针到开始位置
	if _, err = tmpFile.Seek(0, 0); err != nil {
		u.Error("重置文件指针失败", zap.Error(err))
		return
	}

	// 上传头像
	if err = u.uploadUserAvatar(tmpFile, uid); err != nil {
		u.Error("上传用户头像失败", zap.Error(err))
		return
	}

	// 通知好友头像更新
	if err = u.notifyAvatarUpdate(uid); err != nil {
		u.Error("通知好友头像更新失败", zap.Error(err))
	}
}

// 上传用户头像
func (u *User) uploadUserAvatar(file *os.File, uid string) error {
	avatarID := crc32.ChecksumIEEE([]byte(uid)) % uint32(u.ctx.GetConfig().Avatar.Partition)
	_, err := u.fileService.UploadFile(fmt.Sprintf("avatar/%d/%s.png", avatarID, uid), "image/png", func(w io.Writer) error {
		_, err := io.Copy(w, file)
		return err
	})
	if err != nil {
		return err
	}

	// 更新用户头像状态
	return u.db.UpdateUsersWithField("is_upload_avatar", "1", uid)
}

// 通知好友头像更新
func (u *User) notifyAvatarUpdate(uid string) error {
	friends, err := u.friendDB.QueryFriends(uid)
	if err != nil {
		return err
	}

	if len(friends) > 0 {
		uids := make([]string, 0, len(friends))
		for _, friend := range friends {
			uids = append(uids, friend.ToUID)
		}

		return u.ctx.SendCMD(config.MsgCMDReq{
			CMD:         common.CMDUserAvatarUpdate,
			Subscribers: uids,
			Param: map[string]interface{}{
				"uid": uid,
			},
		})
	}
	return nil
}

// 验证业务系统token
func (u *User) verifyBusinessTokenFromHeader(c *wkhttp.Context) error {
	businessToken := c.GetHeader(BusinessAuthHeaderKey)
	if businessToken == "" {
		return ErrBusinessTokenMissing
	}
	if !u.verifyBusinessToken(businessToken) {
		return ErrBusinessTokenInvalid
	}
	return nil
}

func (u *User) verifyBusinessToken(token string) bool {
	businessTokens := u.ctx.GetConfig().Business.Tokens
	if len(businessTokens) == 0 {
		return false
	}
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
	Sex      int        `json:"sex"`      // 性别 0:未知 1:男 2:女
	Flag     uint8      `json:"flag"`     // 设备标记 0.APP 1.PC
	Device   *deviceReq `json:"device"`   // 设备信息
	Zone     string     `json:"zone"`     // 区号
	Phone    string     `json:"phone"`    // 手机号
	Username string     `json:"username"` // 用户名
	Avatar   string     `json:"avatar"`   // 头像链接
}

func (r *businessLoginReq) Check() error {
	if r.Name == "" {
		return errors.New("用户昵称不能为空")
	}
	if r.Sex != 0 && r.Sex != 1 && r.Sex != 2 {
		return errors.New("性别参数错误")
	}
	return nil
}

// 业务系统修改用户信息
func (u *User) businessUpdateUser(c *wkhttp.Context) {
	// 验证业务系统token
	if err := u.verifyBusinessTokenFromHeader(c); err != nil {
		c.ResponseError(err)
		return
	}

	username := c.Param("username")
	if username == "" {
		c.ResponseError(errors.New("用户名不能为空"))
		return
	}

	var req businessUpdateUserReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(ErrInvalidRequestData)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	// 查询用户信息
	userInfo, err := u.db.QueryByUsername(username)
	if err != nil {
		u.Error("查询用户信息出错", zap.Error(err))
		c.ResponseError(errors.New("查询用户信息出错"))
		return
	}
	if userInfo == nil {
		c.ResponseError(ErrUserNotFound)
		return
	}

	if err := u.handleBusinessUserUpdate(userInfo.UID, &req); err != nil {
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

// 处理业务系统用户信息更新
func (u *User) handleBusinessUserUpdate(uid string, req *businessUpdateUserReq) error {
	tx, err := u.db.session.Begin()
	if err != nil {
		u.Error("创建事务失败", zap.Error(err))
		return ErrDBTransaction
	}
	defer tx.Rollback()

	// 更新用户基本信息
	if err := u.updateUserBasicInfo(tx, uid, req); err != nil {
		return err
	}

	// 处理头像上传
	if req.Avatar != "" {
		if err := u.handleBusinessAvatarUpdate(uid, req.Avatar); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		u.Error("数据库事务提交失败", zap.Error(err))
		return ErrDBTransaction
	}

	return nil
}

// 更新用户基本信息
func (u *User) updateUserBasicInfo(_ *dbr.Tx, uid string, req *businessUpdateUserReq) error {
	if req.Name != "" {
		if err := u.db.UpdateUsersWithField("name", req.Name, uid); err != nil {
			u.Error("修改用户名称失败", zap.Error(err))
			return errors.New("修改用户名称失败")
		}
	}
	if req.Sex != 0 {
		if err := u.db.UpdateUsersWithField("sex", fmt.Sprintf("%d", req.Sex), uid); err != nil {
			u.Error("修改用户性别失败", zap.Error(err))
			return errors.New("修改用户性别失败")
		}
	}
	return nil
}

// 处理业务系统头像更新
func (u *User) handleBusinessAvatarUpdate(uid string, avatarURL string) error {
	reader, err := u.fileService.DownloadImage(avatarURL, context.Background())
	if err != nil {
		u.Error("下载头像失败", zap.Error(err))
		return errors.New("下载头像失败")
	}
	defer reader.Close()

	tmpFile, err := os.CreateTemp("", "avatar-*.png")
	if err != nil {
		u.Error("创建临时文件失败", zap.Error(err))
		return errors.New("创建临时文件失败")
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err = io.Copy(tmpFile, reader); err != nil {
		u.Error("保存头像数据失败", zap.Error(err))
		return errors.New("保存头像数据失败")
	}

	if _, err = tmpFile.Seek(0, 0); err != nil {
		u.Error("重置文件指针失败", zap.Error(err))
		return errors.New("重置文件指针失败")
	}

	if err = u.uploadUserAvatar(tmpFile, uid); err != nil {
		u.Error("上传用户头像失败", zap.Error(err))
		return errors.New("上传用户头像失败")
	}

	if err = u.notifyAvatarUpdate(uid); err != nil {
		u.Error("通知好友头像更新失败", zap.Error(err))
		return errors.New("通知好友头像更新失败")
	}

	return nil
}

type businessUpdateUserReq struct {
	Name   string `json:"name"`   // 用户昵称
	Sex    int    `json:"sex"`    // 性别 0:未知 1:男 2:女
	Avatar string `json:"avatar"` // 头像链接
}

func (r *businessUpdateUserReq) Check() error {
	if r.Name == "" && r.Sex == 0 && r.Avatar == "" {
		return errors.New("请至少提供一个需要修改的字段")
	}
	if r.Sex != 0 && r.Sex != 1 && r.Sex != 2 {
		return errors.New("性别参数错误")
	}
	return nil
}
