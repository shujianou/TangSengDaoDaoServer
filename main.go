// @title TangSengDaoDao API
// @version 1.0
// @description TangSengDaoDao Server API documentation
// @BasePath /
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"

	_ "github.com/TangSengDaoDao/TangSengDaoDaoServer/docs"
	_ "github.com/TangSengDaoDao/TangSengDaoDaoServer/internal"
	"github.com/TangSengDaoDao/TangSengDaoDaoServer/modules/base/event"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/config"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/module"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/log"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/server"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"github.com/robfig/cron"
	"github.com/spf13/viper"
)

// go ldflags
var Version string    // version
var Commit string     // git commit id
var CommitDate string // git commit date
var TreeState string  // git tree state

func loadConfigFromFile(cfgFile string) *viper.Viper {
	vp := viper.New()
	vp.SetConfigFile(cfgFile)
	if err := vp.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", vp.ConfigFileUsed())
	}
	return vp
}

func main() {
	var CfgFile string //config file
	flag.StringVar(&CfgFile, "config", "configs/tsdd.yaml", "config file")
	flag.Parse()
	vp := loadConfigFromFile(CfgFile)
	vp.SetEnvPrefix("ts")
	vp.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	vp.AutomaticEnv()

	gin.SetMode(gin.ReleaseMode)

	cfg := config.New()
	cfg.Version = Version
	cfg.ConfigureWithViper(vp)

	// 初始化context
	ctx := config.NewContext(cfg)
	ctx.Event = event.New(ctx)

	logOpts := log.NewOptions()
	logOpts.Level = cfg.Logger.Level
	logOpts.LineNum = cfg.Logger.LineNum
	logOpts.LogDir = cfg.Logger.Dir
	log.Configure(logOpts)

	var serverType string
	if len(os.Args) > 1 {
		serverType = strings.TrimSpace(os.Args[1])
		serverType = strings.Replace(serverType, "-", "", -1)
	}

	if serverType == "api" || serverType == "" || serverType == "config" { // api服务启动
		runAPI(ctx)
	}

}

func runAPI(ctx *config.Context) {
	// 创建server
	s := server.New(ctx)
	ctx.SetHttpRoute(s.GetRoute())
	// 替换web下的配置文件
	replaceWebConfig(ctx.GetConfig())
	// 初始化api
	s.GetRoute().UseGin(ctx.Tracer().GinMiddle()) // 需要放在 api.Route(s.GetRoute())的前面
	s.GetRoute().UseGin(func(c *gin.Context) {
		ingorePaths := ingorePaths()
		for _, ingorePath := range ingorePaths {
			if ingorePath == c.FullPath() {
				return
			}
		}
		gin.Logger()(c)
	})

	// 模块安装
	err := module.Setup(ctx)
	if err != nil {
		panic(err)
	}
	//开始定时处理事件
	cn := cron.New()
	//定时发布事件 每59秒执行一次
	err = cn.AddFunc("0/59 * * * * ?", func() {
		ctx.Event.(*event.Event).EventTimerPush()
	})
	if err != nil {
		panic(err)
	}
	cn.Start()

	// 打印服务器信息
	printServerInfo(ctx)

	// 运行
	err = svc.Run(s)
	if err != nil {
		panic(err)
	}
}

func printServerInfo(ctx *config.Context) {
	infoStr := `
	Hello IM
	`
	cfg := ctx.GetConfig()
	infoStr = strings.Replace(infoStr, "#mode#", string(cfg.Mode), -1)
	infoStr = strings.Replace(infoStr, "#appname#", cfg.AppName, -1)
	infoStr = strings.Replace(infoStr, "#version#", cfg.Version, -1)
	infoStr = strings.Replace(infoStr, "#git#", fmt.Sprintf("%s-%s", CommitDate, Commit), -1)
	infoStr = strings.Replace(infoStr, "#gobuild#", runtime.Version(), -1)
	infoStr = strings.Replace(infoStr, "#fileService#", cfg.FileService.String(), -1)
	infoStr = strings.Replace(infoStr, "#imurl#", cfg.WuKongIM.APIURL, -1)
	infoStr = strings.Replace(infoStr, "#apiAddr#", cfg.Addr, -1)
	infoStr = strings.Replace(infoStr, "#configPath#", cfg.ConfigFileUsed(), -1)
	fmt.Println(infoStr)
}

func ingorePaths() []string {

	return []string{
		"/v1/robots/:robot_id/:app_key/events",
		"/v1/ping",
	}
}

func replaceWebConfig(cfg *config.Config) {
	path := "./assets/web/js/config.js"
	newConfigContent := fmt.Sprintf(`const apiURL = "%s/"`, cfg.External.APIBaseURL)
	ioutil.WriteFile(path, []byte(newConfigContent), 0)

}
