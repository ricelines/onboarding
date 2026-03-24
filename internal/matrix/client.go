package matrix

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
)

var ErrUserAlreadyExists = errors.New("matrix user already exists")

const registrationTokenAuthType = mautrix.AuthType("m.login.registration_token")

type registrationTokenAuthData struct {
	mautrix.BaseAuthData
	Token string `json:"token"`
}

type CreateUserResult struct {
	UserID string
}

type LoginUserResult struct {
	UserID string
}

type Client struct {
	homeserverURL     string
	registrationToken string
	newClient         func(string) (*mautrix.Client, error)
}

func NewClient(homeserverURL, registrationToken string) *Client {
	return &Client{
		homeserverURL:     strings.TrimRight(homeserverURL, "/"),
		registrationToken: strings.TrimSpace(registrationToken),
		newClient: func(homeserverURL string) (*mautrix.Client, error) {
			return mautrix.NewClient(homeserverURL, "", "")
		},
	}
}

func (c *Client) CreateUser(ctx context.Context, username, password string) (CreateUserResult, error) {
	client, err := c.newClient(c.homeserverURL)
	if err != nil {
		return CreateUserResult{}, fmt.Errorf("build registration client: %w", err)
	}

	registerReq := &mautrix.ReqRegister{
		Username:     username,
		Password:     password,
		InhibitLogin: true,
	}

	resp, uia, err := client.Register(ctx, registerReq)
	if err != nil && uia == nil {
		if errors.Is(err, mautrix.MUserInUse) {
			return CreateUserResult{}, ErrUserAlreadyExists
		}
		return CreateUserResult{}, fmt.Errorf("start register user: %w", err)
	}
	if resp == nil {
		auth, authErr := buildRegistrationAuth(uia, c.registrationToken)
		if authErr != nil {
			return CreateUserResult{}, authErr
		}
		registerReq.Auth = auth

		resp, uia, err = client.Register(ctx, registerReq)
		if err != nil && uia == nil {
			if errors.Is(err, mautrix.MUserInUse) {
				return CreateUserResult{}, ErrUserAlreadyExists
			}
			return CreateUserResult{}, fmt.Errorf("complete register user: %w", err)
		}
		if resp == nil {
			if uia != nil && uia.Error != "" {
				return CreateUserResult{}, fmt.Errorf("registration did not complete: %s", uia.Error)
			}
			return CreateUserResult{}, errors.New("registration did not complete")
		}
	}

	return CreateUserResult{UserID: resp.UserID.String()}, nil
}

func (c *Client) LoginUser(ctx context.Context, username, password string) (LoginUserResult, error) {
	client, err := c.newClient(c.homeserverURL)
	if err != nil {
		return LoginUserResult{}, fmt.Errorf("build login client: %w", err)
	}

	resp, err := client.Login(ctx, &mautrix.ReqLogin{
		Type: mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{
			Type: mautrix.IdentifierTypeUser,
			User: username,
		},
		Password:         password,
		StoreCredentials: false,
	})
	if err != nil {
		return LoginUserResult{}, fmt.Errorf("login user: %w", err)
	}
	return LoginUserResult{UserID: resp.UserID.String()}, nil
}

func buildRegistrationAuth(uia *mautrix.RespUserInteractive, registrationToken string) (any, error) {
	if uia == nil {
		return nil, errors.New("homeserver did not provide a registration auth flow")
	}
	if strings.TrimSpace(registrationToken) != "" {
		if uia.HasSingleStageFlow(registrationTokenAuthType) {
			return registrationTokenAuthData{
				BaseAuthData: mautrix.BaseAuthData{Type: registrationTokenAuthType, Session: uia.Session},
				Token:        registrationToken,
			}, nil
		}
		if uia.HasSingleStageFlow(mautrix.AuthTypeDummy) {
			return mautrix.BaseAuthData{Type: mautrix.AuthTypeDummy, Session: uia.Session}, nil
		}
		return nil, fmt.Errorf("homeserver does not accept registration tokens for this flow")
	}
	if uia.HasSingleStageFlow(mautrix.AuthTypeDummy) {
		return mautrix.BaseAuthData{Type: mautrix.AuthTypeDummy, Session: uia.Session}, nil
	}
	if uia.HasSingleStageFlow(registrationTokenAuthType) {
		return nil, errors.New("homeserver requires a registration token for account creation, but the provisioner was started without one")
	}
	return nil, errors.New("unsupported registration auth flow")
}
