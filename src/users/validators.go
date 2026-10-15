package users

import (
	"errors"
	"net/mail"
)

func (createUserReq *CreateUserRequest) ValidateCreateUserRequest() error {
	if _, err := mail.ParseAddress(createUserReq.Email); err != nil {
		return errors.New("a valid email is required")
	}

	if createUserReq.FirstName == "" {
		return errors.New("first name is required")
	}

	if createUserReq.LastName == "" {
		return errors.New("lastname is required")
	}

	if createUserReq.Username == "" {
		return errors.New("username is required")
	}

	return nil
}
