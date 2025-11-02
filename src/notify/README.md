# Notify 通知模块

## 功能说明

该模块提供统一的通知发送功能，支持以下通知方式：
- Telegram 消息通知
- Email 邮件通知
- Gotify 消息推送

## 使用方法

### 发送telegram通知

https://core.telegram.org/bots#6-botfather

#### 示例展示

![image-20250922002403000](./assets/image-20250922002403000.png)



### 发送email测试通知(QQ邮箱示例)

https://wx.mail.qq.com/list/readtemplate?name=app_intro.html#/agreement/authorizationCode



#### 示例展示

![image-20250922002456095](./assets/image-20250922002456095.png)

### 发送Gotify推送通知

Gotify是一个简单的自托管推送通知服务器，支持PC和手机客户端。

官方网站：https://gotify.net/

#### 配置步骤

1. 搭建或使用现有的Gotify服务器
2. 在Gotify服务器中创建一个应用，获取应用Token
3. 在配置文件中填写Gotify服务器地址和Token

## 配置说明

在配置文件中启用相应的通知服务：

```yaml
# 通知服务配置
notify:
  telegram:
    enable: true                # 是否启用Telegram通知
    withNotification: true      # 是否在Telegram通知中包含通知内容（是否有声音通知）
    botToken: "YOUR_BOT_TOKEN"  # Telegram机器人的Token
    chatID: "YOUR_CHAT_ID"      # 接收通知的Chat ID
  
  email:
    enable: true                # 是否启用邮件通知
    smtpHost: "smtp.example.com" # SMTP服务器地址
    smtpPort: 465               # SMTP服务器端口
    senderEmail: "sender@example.com"    # 发送者邮箱
    senderPassword: "password"  # 发送者邮箱密码或授权码
    recipientEmail: "recipient@example.com"  # 接收者邮箱
  
  gotify:
    enable: true                # 是否启用Gotify通知
    serverURL: "http://192.168.1.100:8080"  # Gotify服务器地址
    token: "YOUR_APP_TOKEN"     # Gotify应用Token
    priority: 5                 # 消息优先级 (0-10, 数值越大优先级越高)
```

## 注意事项

1. 请确保在使用通知功能前已正确配置相关参数
2. 函数会自动检测启用的通知方式并发送，如果某种通知方式发送失败，不会影响其他通知方式的发送
3. 邮件通知使用SMTP协议发送，请确保SMTP服务器配置正确
4. Gotify通知需要先搭建Gotify服务器，推荐使用Docker快速部署
