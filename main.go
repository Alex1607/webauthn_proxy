package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"path/filepath"
	"time"

	u "github.com/Quiq/webauthn_proxy/user"
	util "github.com/Quiq/webauthn_proxy/util"

	"github.com/duo-labs/webauthn.io/session"
	"github.com/duo-labs/webauthn/webauthn"
	"github.com/gorilla/sessions"

	"github.com/spf13/viper"

	yaml "gopkg.in/yaml.v3"
)

var (
	configuration        Configuration
	loginError           WebAuthnError
	registrationError    WebAuthnError
	registrations        map[string]u.User
	sessionStoreKey      []byte
	sessionStore         *sessions.CookieStore
	users                map[string]u.User
	webAuthn             *webauthn.WebAuthn
	webauthnSessionStore *session.Store
)

type Configuration struct {
	CredentialFile string

	EnableFullRegistration bool

	RPDisplayName string // Relying party display name
	RPID          string // Relying party ID
	RPOrigin      string // Relying party origin

	ServerAddress        string
	ServerPort           string
	SessionLengthSeconds int
	StaticPath           string

	UsernameRegex string
}

type RegistrationSuccess struct {
	Message string
	Data    string
}

type AuthenticationSuccess struct {
	Message string
}

type AuthenticationFailure struct {
	Message string
}

type WebAuthnError struct {
	Message string
}

type Credentials map[string]string
type WebAuthnCredentials map[string]webauthn.Credential

func main() {
	var err error
	var credfile []byte
	var credentials map[string]string

	loginError = WebAuthnError{Message: "Unable to login"}
	registrationError = WebAuthnError{Message: "Error during registration"}

	rand.Seed(time.Now().UnixNano())

	users = make(map[string]u.User)
	registrations = make(map[string]u.User)

	viper.SetDefault("configpath", "/opt/webauthn_proxy")
	viper.SetEnvPrefix("webauthn_proxy")
	viper.BindEnv("configpath")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")

	// Set configuration defaults
	viper.SetDefault("credentialfile", "/opt/webauthn_proxy/credentials.yml")
	viper.SetDefault("enablefullregistration", false)
	viper.SetDefault("serveraddress", "127.0.0.1")
	viper.SetDefault("serverport", "8080")
	viper.SetDefault("sessionlengthseconds", 86400)
	viper.SetDefault("staticpath", "/static/")
	viper.SetDefault("usernameregex", "^.*$")

	configpath := viper.GetString("configpath")
	log.Printf("Reading config file, %s/config.yml", configpath)
	viper.AddConfigPath(configpath)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("\nError reading config file %s/config.yml. %s", configpath, err.Error())
	}

	if err = viper.Unmarshal(&configuration); err != nil {
		log.Fatalln("Unable to decode config file into struct.", err)
	}

	fmt.Printf("\nCredential File: %s", configuration.CredentialFile)
	fmt.Printf("\nRelying Party Display Name: %s", configuration.RPDisplayName)
	fmt.Printf("\nRelying Party ID: %s", configuration.RPID)
	fmt.Printf("\nRelying Party Origin: %s", configuration.RPOrigin)
	fmt.Printf("\nEnable Full Registration: %v", configuration.EnableFullRegistration)
	fmt.Printf("\nServer Address: %s", configuration.ServerAddress)
	fmt.Printf("\nServer Port: %s", configuration.ServerPort)
	fmt.Printf("\nSession Length: %d", configuration.SessionLengthSeconds)
	fmt.Printf("\nStatic Path: %s", configuration.StaticPath)
	fmt.Printf("\nUsername Regex: %s\n\n", configuration.UsernameRegex)

	if credfile, err = ioutil.ReadFile(configuration.CredentialFile); err != nil {
		log.Fatalf("\nUnable to read credential file %s %v", configuration.CredentialFile, err)
	}

	if err = yaml.Unmarshal(credfile, &credentials); err != nil {
		log.Fatalf("\nUnable to parse YAML credential file %s %v", configuration.CredentialFile, err)
	}

	// Unmarshall credentials map to users
	for username, credential := range credentials {
		unmarshaledUser, err := u.UnmarshalUser(credential)
		if err != nil {
			log.Fatalf(fmt.Sprintf("Error unmarshalling user credential %s", username), err)
		}

		users[username] = *unmarshaledUser
	}

	webAuthn, err = webauthn.New(&webauthn.Config{
		RPDisplayName: configuration.RPDisplayName, // Relying party display name
		RPID:          configuration.RPID,          // Relying party ID
		RPOrigin:      configuration.RPOrigin,      // Relying party origin
	})

	if err != nil {
		log.Fatalln("Failed to create WebAuthn from config:", err)
	}

	webauthnSessionStore, err = session.NewStore()
	if err != nil {
		log.Fatalln("Failed to create Webauthn session store:", err)
	}

	sessionStoreKey = util.RandStringBytesRmndr(32)
	sessionStore = sessions.NewCookieStore(sessionStoreKey)

	// Sessions stored for a configurable length of time
	sessionStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   configuration.SessionLengthSeconds,
		HttpOnly: true,
	}

	r := http.NewServeMux()

	r.HandleFunc("/webauthn/auth", GetUserAuth)
	r.HandleFunc("/webauthn/login", HandleLogin)
	r.HandleFunc("/webauthn/register", HandleRegister)
	r.HandleFunc("/webauthn/login/get_credential_request_options", GetCredentialRequestOptions)
	r.HandleFunc("/webauthn/login/process_login_assertion", ProcessLoginAssertion)
	r.HandleFunc("/webauthn/register/get_credential_creation_options", GetCredentialCreationOptions)
	r.HandleFunc("/webauthn/register/process_registration_attestation", ProcessRegistrationAttestation)

	// All remaining references to static assets. Add /webauthn_static/ for embedding.
	r.Handle("/webauthn_static/", http.StripPrefix("/webauthn_static/", http.FileServer(http.Dir(configuration.StaticPath))))
	r.Handle("/", http.FileServer(http.Dir(configuration.StaticPath)))

	serverAddress := fmt.Sprintf("%s:%s", configuration.ServerAddress, configuration.ServerPort)
	log.Println("Starting server at", serverAddress)
	log.Fatalln(http.ListenAndServe(serverAddress, r))
}

func GetUserAuth(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionStore.Get(r, "webauthn-proxy-session")

	// Check if user is authenticated
	if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
		util.JSONResponse(w, AuthenticationFailure{Message: "Unauthenticated"}, http.StatusUnauthorized)
		return
	} else {
		util.JSONResponse(w, AuthenticationSuccess{Message: "OK"}, http.StatusOK)
		return
	}
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionStore.Get(r, "webauthn-proxy-session")
	redirectUrl := util.GetRedirectUrl(r, "/webauthn_static/authenticated.html")

	if auth, ok := session.Values["authenticated"].(bool); ok && auth {
		http.Redirect(w, r, redirectUrl, http.StatusFound)
	} else {
		http.ServeFile(w, r, filepath.Join(configuration.StaticPath, "login.html"))
	}

	return
}

func HandleRegister(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(configuration.StaticPath, "register.html"))
	return
}

// Step 1 of the login process, get credential request options for the user
func GetCredentialRequestOptions(w http.ResponseWriter, r *http.Request) {
	username, err := util.GetUsername(r, configuration.UsernameRegex)
	if err != nil {
		log.Println("Error getting username", err)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	user, exists := users[username]
	if !exists {
		log.Printf("\nUser %s does not exist", username)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	// Begin the login process
	options, sessionData, err := webAuthn.BeginLogin(user)
	if err != nil {
		log.Println("Error beginning the login process", err)
		util.JSONResponse(w, loginError, http.StatusInternalServerError)
		return
	}

	// Store session data as marshaled JSON
	err = webauthnSessionStore.SaveWebauthnSession("authentication", sessionData, r, w)
	if err != nil {
		log.Println("Error saving Webauthn session during login", err)
		util.JSONResponse(w, loginError, http.StatusInternalServerError)
		return
	}

	util.JSONResponse(w, options, http.StatusOK)
	return
}

// Step 2 of the login process, process the assertion from the client authenticator
func ProcessLoginAssertion(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionStore.Get(r, "webauthn-proxy-session")
	username, err := util.GetUsername(r, configuration.UsernameRegex)
	if err != nil {
		log.Println("Error getting username", err)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	user, exists := users[username]
	if !exists {
		log.Printf("\nUser %s does not exist", username)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	// Load the session data
	sessionData, err := webauthnSessionStore.GetWebauthnSession("authentication", r)
	if err != nil {
		log.Println("Error getting Webauthn session during login", err)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	cred, err := webAuthn.FinishLogin(user, sessionData, r)
	if err != nil {
		log.Println("Error finishing Webauthn login", err)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	// Check for cloned authenticators
	if cred.Authenticator.CloneWarning {
		log.Printf("\nError. Authenticator for %s appears to be cloned, failing login", username)
		util.JSONResponse(w, loginError, http.StatusBadRequest)
		return
	}

	// Increment sign count on user to help avoid clones
	// !!! For now this is okay because we only allow a user to register once.
	// !!! In the future we will have to make sure we are updating the correct cred.
	user.Credentials[0].Authenticator.UpdateCounter(cred.Authenticator.SignCount)

	// Set user as authenticated
	session.Values["authenticated"] = true
	session.Save(r, w)

	successMessage := AuthenticationSuccess{
		Message: "Authentication Successful",
	}
	util.JSONResponse(w, successMessage, http.StatusOK)
	return
}

// Step 1 of the registration process, get credential creation options
func GetCredentialCreationOptions(w http.ResponseWriter, r *http.Request) {
	username, err := util.GetUsername(r, configuration.UsernameRegex)
	if err != nil {
		log.Println("Error getting username", err)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	// Currently not registering a user more than once, but we may want
	// to allow this, so that users can use multiple authenticators.
	// Just need to make sure the same authenticator isn't registered
	// multiple times.
	if _, exists := users[username]; exists {
		log.Printf("\nUser %s is already registered", username)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	} else if _, exists = registrations[username]; exists {
		log.Printf("\nUser %s has already begun registration", username)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	user := u.NewUser(username)
	registrations[username] = *user

	// Generate PublicKeyCredentialCreationOptions, session data
	options, sessionData, err := webAuthn.BeginRegistration(
		user,
	)

	if err != nil {
		log.Println("Error beginning Webauthn registration", err)
		util.JSONResponse(w, registrationError, http.StatusInternalServerError)
		return
	}

	// Store session data as marshaled JSON
	if err = webauthnSessionStore.SaveWebauthnSession("registration", sessionData, r, w); err != nil {
		log.Println("Error saving Webauthn session during registration", err)
		util.JSONResponse(w, registrationError, http.StatusInternalServerError)
		return
	}

	util.JSONResponse(w, options, http.StatusOK)
	return
}

// Step 2 of the registration process, process the attestation (new credential) from the client authenticator
func ProcessRegistrationAttestation(w http.ResponseWriter, r *http.Request) {
	var user u.User
	username, err := util.GetUsername(r, configuration.UsernameRegex)
	if err != nil {
		log.Println("Error getting username", err)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	if _, exists := users[username]; exists {
		log.Printf("\nUser %s is already registered", username)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	} else if user, exists = registrations[username]; !exists {
		log.Printf("\nUser %s has not begun registration", username)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	// Load the session data
	sessionData, err := webauthnSessionStore.GetWebauthnSession("registration", r)
	if err != nil {
		log.Println("Error getting Webauthn session during registration", err)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	credential, err := webAuthn.FinishRegistration(user, sessionData, r)
	if err != nil {
		log.Println("Error finishing Webauthn registration", err)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	// Check that the credential doesn't belong to another user
	for _, value := range users {
		// !!! Making the assumption that users have 1 credential for now.
		// !!! This won't always be true, if we allow users to register
		// !!! multiple authenticators.
		if bytes.Compare(value.Credentials[0].ID, credential.ID) == 0 {
			log.Printf("\nError registering credential for user %s, matching credential ID with user %s", username, value.Name)
			util.JSONResponse(w, registrationError, http.StatusBadRequest)
			return
		}
	}

	// Add the credential to the user.
	user.AddCredential(*credential)

	// Note: enabling this can be risky as it allows anyone to add themselves to the proxy.
	// Only enable full registration if the registration page is secure (e.g. behind
	// some other form of authentication)
	if configuration.EnableFullRegistration {
		users[username] = user
	}

	// Marshal the user so it can be added to the credentials file
	marshaledUser, err := u.MarshalUser(user)
	if err != nil {
		log.Println("Error marshalling user object", err)
		util.JSONResponse(w, registrationError, http.StatusBadRequest)
		return
	}

	successMessage := RegistrationSuccess{
		Message: "Registration Successful. Please share the values below with your system administrator so they can add you to the credential file:",
		Data:    fmt.Sprintf("%s: %s", username, marshaledUser),
	}
	util.JSONResponse(w, successMessage, http.StatusOK)
	return
}
