# GSB Bugfix Task

## 题面

Go 实现的 sudo 策略解析器在迁移后放行了不该放行的命令。别名展开只做了一层；命令匹配按前缀而不是完整路径；参数通配符匹配过宽；用户组嵌套没有递归；Host 限制被忽略；NOPASSWD 标签继承错误；runas 用户列表为空时默认 root；include 指令可以引入任意文件；否定规则优先级错误；命令里的通配符可以注入 shell；策略缓存没有失效；解析失败后回退到允许。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
