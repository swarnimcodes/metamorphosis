package exercises

import (
	"errors"
)

func (createExerciseReq *CreateExerciseRequest) ValidateCreateExerciseRequest() error {

	if createExerciseReq.Name == "" {
		return errors.New("exercise name is required")
	}

	return nil
}
