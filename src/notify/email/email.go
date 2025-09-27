package email

import (
	"fmt"

	"github.com/bililive-go/bililive-go/src/configs"
	"gopkg.in/gomail.v2"
)

// EmailMessage represents an email message
type EmailMessage struct {
	Subject string
	Body    string
}

// SendEmail 发送邮件 subject 主题 body 内容
func SendEmail(subject, body string) error {

	cfg := configs.GetCurrentConfig()
	emailConfig := cfg.Notify.Email

	m := gomail.NewMessage()
	m.SetHeader("From", emailConfig.SenderEmail)
	m.SetHeader("To", emailConfig.RecipientEmail)
	m.SetHeader("Subject", subject)
	m.SetBody("text/plain", body)

	d := gomail.NewDialer(
		emailConfig.SMTPHost,
		emailConfig.SMTPPort,
		emailConfig.SenderEmail,
		emailConfig.SenderPassword,
	)

	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	fmt.Println("Email sent successfully!")
	return nil
}
