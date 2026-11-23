# GSB Bugfix Task

## 题面

Kerberos 网关接入新 AD 域后，PAC 校验出现越权。PAC 签名只验了 KDC 校验和，没有验服务校验和；PAC 里的组 SID 没有和域信任关系比对；跨域引用票的路径没有限制，能绕到不信任的域；PAC 过期后仍被接受；同一票据并发校验，缓存互相覆盖； delegates 标志没有校验，普通服务拿到转发权限；PAC 里的日志清算信息被忽略；票据加密类型允许 RC4；服务主体名没有规范化，大小写不同被当成两个；重放缓存按票据而不是按 authenticator；时钟偏差留了十五分钟；重启后重放缓存丢失。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 身份认证
- type: Bugfix
- difficulty: 地狱
