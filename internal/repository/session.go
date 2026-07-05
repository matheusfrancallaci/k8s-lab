package repository

import (
	"sync"

	"estudo-app/internal/models"

	"github.com/google/uuid"
)

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*models.QuizSession
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*models.QuizSession),
	}
}

func (s *SessionStore) Create(questions []models.Question, cert, difficulty string) (string, *models.QuizSession) {
	id := uuid.New().String()
	session := &models.QuizSession{
		Questions:  questions,
		Current:    0,
		Answers:    make([]int, len(questions)),
		Cert:       cert,
		Difficulty: difficulty,
	}
	for i := range session.Answers {
		session.Answers[i] = -1
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	return id, session
}

func (s *SessionStore) Get(id string) (*models.QuizSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *SessionStore) Answer(id string, answer int) (*models.QuizSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}

	if sess.Current < len(sess.Questions) {
		sess.Answers[sess.Current] = answer
		if answer == sess.Questions[sess.Current].Answer {
			sess.Score++
		}
		sess.Current++
	}

	return sess, true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}
