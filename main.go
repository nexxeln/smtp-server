package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"time"
)

// structure for the email request payload
type EmailRequest struct {
	Subject    string   `json:"subject"`
	Message    string   `json:"message"`
	Recipients []string `json:"recipients"`
}

// handles the incoming HTTP request to send an email
func sendEmailHandler(w http.ResponseWriter, r *http.Request) {
	// restrict to only POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// decode the request payload
	var request EmailRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// validate recipient email addresses
	for _, recipient := range request.Recipients {
		if !isValidEmail(recipient) {
			http.Error(w, fmt.Sprintf("Recipient email address '%s' is not valid", recipient), http.StatusBadRequest)
			return
		}
	}

	emailConfig, err := getEmailConfig()
	if err != nil {
		log.Fatal(err)
	}

	// send emails in the background
	go func() {
		// authenticate with the SMTP server
		auth := smtp.PlainAuth("", emailConfig.senderEmail, emailConfig.password, emailConfig.smtpServer)
		// format the SMTP server address
		addr := fmt.Sprintf("%s:%s", emailConfig.smtpServer, emailConfig.smtpPort)

		msg := formatEmailMessage(request.Recipients, request.Subject, request.Message)

		maxRetries := 3
		retryCount := 0
		backoff := 1 * time.Second

		for {
			if err := smtp.SendMail(addr, auth, emailConfig.senderEmail, request.Recipients, msg); err != nil {
				retryCount++
				if retryCount >= maxRetries {
					log.Printf("Failed to send email after multiple attempts: %v", err)
					return
				}
				log.Printf("Attempt %d failed, retrying in %v...\n", retryCount, backoff)
				time.Sleep(backoff)
				backoff *= 2
			} else {
				log.Println("Email sent successfully")
				break
			}
		}
	}()

	// respond immediately
	w.Header().Set("Content-Type", "text/plain")
	// 202 Accepted because the email is being processed in the background
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("Email is being processed"))
}

// structure to store email configuration
type emailConfig struct {
	senderEmail string
	password    string
	smtpServer  string
	smtpPort    string
}

// get email configuration from environment variables
func getEmailConfig() (emailConfig, error) {
	config := emailConfig{
		senderEmail: os.Getenv("SENDER_EMAIL"),
		password:    os.Getenv("EMAIL_PASSWORD"),
		smtpServer:  os.Getenv("SMTP_SERVER"),
		smtpPort:    os.Getenv("SMTP_PORT"),
	}

	if config.senderEmail == "" || config.password == "" || config.smtpServer == "" || config.smtpPort == "" {
		return emailConfig{}, fmt.Errorf("one or more environment variables are not set")
	}

	if !isValidEmail(config.senderEmail) {
		return emailConfig{}, fmt.Errorf("sender email address is not valid")
	}

	return config, nil
}

// check if the provided email address is valid
func isValidEmail(email string) bool {
	const emailRegexPattern = `^([A-Z0-9_+-]+\.?)*[A-Z0-9_+-]@([A-Z0-9][A-Z0-9-]*\.)+[A-Z]{2,}$/i`

	matched, err := regexp.MatchString(emailRegexPattern, email)
	if err != nil {
		return false
	}
	return matched
}

// format the email message
func formatEmailMessage(recipients []string, subject, message string) []byte {
	return []byte(fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s\r\n",
		strings.Join(recipients, ","), subject, message))
}

func main() {
	http.HandleFunc("/send-email", sendEmailHandler)

	log.Println("Server starting on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server start error: %s", err)
	}
}
