package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// structure for the email request payload
type EmailRequest struct {
	Subject    string   `json:"subject"`
	Message    string   `json:"message"`
	Recipients []string `json:"recipients"`
}

var client *mongo.Client

func connectToMongoDB() {
	var err error
	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")
	client, err = mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	err = client.Ping(context.TODO(), nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Connected to MongoDB!")
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

	collection := client.Database("micemail").Collection("emails")

	// validate recipient email addresses
	for _, recipient := range request.Recipients {
		if !isValidEmail(recipient) {
			http.Error(w, fmt.Sprintf("Recipient email address '%s' is not valid", recipient), http.StatusBadRequest)
			return
		}

		// check for duplicate and insert if not exists
		filter := bson.M{"email": recipient}
		var result struct{ Email string }
		err := collection.FindOne(context.TODO(), filter).Decode(&result)
		if err == mongo.ErrNoDocuments {
			_, err := collection.InsertOne(context.TODO(), bson.M{
				"email": recipient,
			})
			if err != nil {
				log.Printf("Could not insert email %s: %v", recipient, err)
			}
		}
	}

	emailConfig, err := getEmailConfig()
	if err != nil {
		log.Fatal(err)
	}

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
				// if max retries reached, return an error response
				http.Error(w, "Failed to send email after multiple attempts", http.StatusInternalServerError)
				return
			}
			// log retry attempt
			log.Printf("Attempt %d failed, retrying in %v...\n", retryCount, backoff)
			time.Sleep(backoff)
			// exponential backoff
			backoff *= 2
		} else {
			break
		}
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent successfully"))
}

// Handler function to get all emails from the database
func getAllEmailsHandler(w http.ResponseWriter, r *http.Request) {
	// restrict to only GET method
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	collection := client.Database("micemail").Collection("emails")

	// find all documents
	cursor, err := collection.Find(context.TODO(), bson.M{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cursor.Close(context.TODO())

	var emails []bson.M
	if err = cursor.All(context.TODO(), &emails); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// set response header to application/json
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// encode and send the emails as JSON
	if err := json.NewEncoder(w).Encode(emails); err != nil {
		log.Printf("Error encoding emails to JSON: %v", err)
	}
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
	connectToMongoDB()
	defer func() {
		if err := client.Disconnect(context.TODO()); err != nil {
			log.Fatalf("Error disconnecting from MongoDB: %s", err)
		}
	}()

	http.HandleFunc("/send-email", sendEmailHandler)
	http.HandleFunc("/get-all-emails", getAllEmailsHandler) // Register the new handler

	log.Println("Server starting on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server start error: %s", err)
	}
}
