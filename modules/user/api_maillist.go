package user

import (
	"fmt"
	"strings"

	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/common"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/util"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/wkhttp"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// 验证用户名是否符合规则
func validateName(name string) error {
	// 检查长度
	if len(name) < 1 {
		return errors.New("用户名长度必须至少为1个字符")
	}
	if len(name) > 32 {
		return errors.New("用户名长度不能超过32个字符")
	}

	return nil
}

// 上传用户通讯录好友
func (u *User) addMaillist(c *wkhttp.Context) {
	loginUID := c.GetLoginUID()
	var req []*mailListReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("请求数据格式有误！"))
		return
	}
	result := make([]*mailListResp, 0)

	if len(req) == 0 {
		c.Response(result)
		return
	}
	loginUser, err := u.db.QueryByUID(loginUID)
	if err != nil {
		c.ResponseError(errors.New("查询登录用户信息错误"))
		return
	}

	// 查询已存在的通讯录记录
	existingMaillist, err := u.maillistDB.query(loginUID)
	if err != nil {
		u.Error("查询用户现有通讯录数据错误", zap.Error(err))
		c.ResponseError(errors.New("查询用户现有通讯录数据错误"))
		return
	}

	// 过滤掉已存在的记录
	newMaillist := make([]*mailListReq, 0)
	for _, maillist := range req {
		isExist := false
		for _, existing := range existingMaillist {
			if existing.Zone == maillist.Zone && existing.Phone == maillist.Phone {
				isExist = true
				break
			}
		}
		if !isExist {
			newMaillist = append(newMaillist, maillist)
		}
	}

	if len(newMaillist) == 0 {
		c.ResponseOK()
		return
	}

	tx, err := u.db.session.Begin()
	if err != nil {
		u.Error("数据库事物开启失败", zap.Error(err))
		c.ResponseError(errors.New("数据库事物开启失败"))
		return
	}
	defer func() {
		if err := recover(); err != nil {
			tx.Rollback()
			panic(err)
		}
	}()

	for _, maillist := range newMaillist {
		// 验证用户名
		if err := validateName(maillist.Name); err != nil {
			tx.RollbackUnlessCommitted()
			c.ResponseError(err)
			return
		}

		zone := maillist.Zone
		if maillist.Zone == "" && !strings.HasPrefix(maillist.Phone, "00") {
			zone = loginUser.Zone
		}
		err := u.maillistDB.insertTx(&maillistModel{
			UID:     loginUID,
			Name:    maillist.Name,
			Zone:    zone,
			Phone:   maillist.Phone,
			Vercode: fmt.Sprintf("%s@%d", util.GenerUUID(), common.MailList),
		}, tx)
		if err != nil {
			tx.RollbackUnlessCommitted()
			u.Error("添加用户通讯录联系人错误", zap.Error(err))
			c.ResponseError(errors.New("添加用户通讯录联系人错误"))
			return
		}
	}
	err = tx.Commit()
	if err != nil {
		tx.Rollback()
		u.Error("数据库事物提交失败", zap.Error(err))
		c.ResponseError(errors.New("数据库事物提交失败"))
		return
	}
	c.ResponseOK()
}

// 获取用户通讯录好友
func (u *User) getMailList(c *wkhttp.Context) {
	loginUID := c.GetLoginUID()
	result := make([]*mailListResp, 0)
	mailLists, err := u.maillistDB.query(loginUID)
	if err != nil {
		u.Error("查询用户通讯录数据错误")
		c.ResponseError(errors.New("查询用户通讯录数据错误"))
		return
	}
	if mailLists == nil {
		c.Response(result)
		return
	}
	phones := make([]string, 0)
	for _, m := range mailLists {
		phones = append(phones, fmt.Sprintf("%s%s", m.Zone, m.Phone))
	}
	users, err := u.db.QueryByPhones(phones)
	if err != nil {
		u.Error("批量查询用户信息错误")
		c.ResponseError(errors.New("批量查询用户信息错误"))
		return
	}
	friends, err := u.friendDB.QueryFriends(loginUID)
	if err != nil {
		u.Error("查询用户好友错误")
		c.ResponseError(errors.New("查询用户好友错误"))
		return
	}
	for _, m := range mailLists {
		var uid = ""
		for _, user := range users {
			if user.Zone == m.Zone && user.Phone == m.Phone {
				uid = user.UID
				break
			}
		}
		if uid == "" {
			continue
		}
		var isFriend = 0
		for _, friend := range friends {
			if uid != "" && friend.ToUID == uid {
				isFriend = 1
				break
			}
		}
		result = append(result, &mailListResp{
			Vercode:  m.Vercode,
			Phone:    m.Phone,
			Name:     m.Name,
			Zone:     m.Zone,
			UID:      uid,
			IsFriend: isFriend,
		})
	}
	c.Response(result)
}

// 添加单条通讯录记录
func (u *User) addSingleMaillist(c *wkhttp.Context) {
	loginUID := c.GetLoginUID()
	var req mailListReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("请求数据格式有误！"))
		return
	}

	// 验证用户名
	if err := validateName(req.Name); err != nil {
		c.ResponseError(err)
		return
	}

	loginUser, err := u.db.QueryByUID(loginUID)
	if err != nil {
		c.ResponseError(errors.New("查询登录用户信息错误"))
		return
	}

	// 查询是否已存在该记录
	existingMaillist, err := u.maillistDB.query(loginUID)
	if err != nil {
		u.Error("查询用户现有通讯录数据错误", zap.Error(err))
		c.ResponseError(errors.New("查询用户现有通讯录数据错误"))
		return
	}

	// 检查是否已存在相同记录
	for _, existing := range existingMaillist {
		if existing.Zone == req.Zone && existing.Phone == req.Phone {
			c.ResponseError(errors.New("该联系人已存在于通讯录中"))
			return
		}
	}

	zone := req.Zone
	if req.Zone == "" && !strings.HasPrefix(req.Phone, "00") {
		zone = loginUser.Zone
	}

	// 开启事务
	tx, err := u.db.session.Begin()
	if err != nil {
		u.Error("数据库事物开启失败", zap.Error(err))
		c.ResponseError(errors.New("数据库事物开启失败"))
		return
	}
	defer func() {
		if err := recover(); err != nil {
			tx.Rollback()
			panic(err)
		}
	}()

	// 插入新记录
	err = u.maillistDB.insertTx(&maillistModel{
		UID:     loginUID,
		Name:    req.Name,
		Zone:    zone,
		Phone:   req.Phone,
		Vercode: fmt.Sprintf("%s@%d", util.GenerUUID(), common.MailList),
	}, tx)
	if err != nil {
		tx.RollbackUnlessCommitted()
		u.Error("添加用户通讯录联系人错误", zap.Error(err))
		c.ResponseError(errors.New("添加用户通讯录联系人错误"))
		return
	}

	err = tx.Commit()
	if err != nil {
		tx.Rollback()
		u.Error("数据库事物提交失败", zap.Error(err))
		c.ResponseError(errors.New("数据库事物提交失败"))
		return
	}
	c.ResponseOK()
}

// 编辑通讯录联系人
func (u *User) updateMaillist(c *wkhttp.Context) {
	loginUID := c.GetLoginUID()
	var req updateMaillistReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("请求数据格式有误！"))
		return
	}

	// 验证用户名
	if err := validateName(req.Name); err != nil {
		c.ResponseError(err)
		return
	}

	// 查询现有通讯录记录
	existingMaillist, err := u.maillistDB.query(loginUID)
	if err != nil {
		u.Error("查询用户现有通讯录数据错误", zap.Error(err))
		c.ResponseError(errors.New("查询用户现有通讯录数据错误"))
		return
	}

	// 查找要修改的联系人
	var targetMaillist *maillistModel
	for _, existing := range existingMaillist {
		if existing.Zone == req.Zone && existing.Phone == req.Phone {
			targetMaillist = existing
			break
		}
	}

	if targetMaillist == nil {
		c.ResponseError(errors.New("未找到该联系人"))
		return
	}

	// 更新联系人姓名
	err = u.maillistDB.updateName(loginUID, req.Zone, req.Phone, req.Name)
	if err != nil {
		u.Error("更新联系人姓名错误", zap.Error(err))
		c.ResponseError(errors.New("更新联系人姓名错误"))
		return
	}

	c.ResponseOK()
}

type mailListReq struct {
	Name  string `json:"name"`
	Zone  string `json:"zone"`
	Phone string `json:"phone"`
}

type mailListResp struct {
	Name     string `json:"name"`
	Zone     string `json:"zone"`
	Phone    string `json:"phone"`
	UID      string `json:"uid"`
	Vercode  string `json:"vercode"`
	IsFriend int    `json:"is_friend"`
}

type updateMaillistReq struct {
	Name  string `json:"name"`  // 新的联系人姓名
	Zone  string `json:"zone"`  // 区号
	Phone string `json:"phone"` // 电话号码
}
