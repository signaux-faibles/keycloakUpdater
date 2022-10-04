package main

import (
	"context"
	"github.com/signaux-faibles/libwekan"
)

func ManageBoardsMembers(wekan libwekan.Wekan, fromConfig Users) error {
	wekanBoardsMembers := fromConfig.selectScopeWekan().inferBoardsMember()
	for boardSlug, boardMembers := range wekanBoardsMembers {
		err := SetMembers(wekan, boardSlug, boardMembers)
		if err != nil {
			return err
		}
	}
	return nil
}

func SetMembers(wekan libwekan.Wekan, boardSlug libwekan.BoardSlug, boardMembers Users) error {
	board, err := wekan.GetBoardFromSlug(context.Background(), boardSlug)
	if err != nil {
		return err
	}
	currentMembersIDs := mapSlice(board.Members, func(member libwekan.BoardMember) libwekan.UserID { return member.UserID })

	// globalWekan.AdminUser() est membre de toutes les boards, ajoutons le ici pour ne pas risquer de l'oublier dans les utilisateurs
	wantedMembersUsernames := []libwekan.Username{wekan.AdminUsername()}
	for username := range boardMembers {
		wantedMembersUsernames = append(wantedMembersUsernames, libwekan.Username(username))
	}
	wantedMembers, err := wekan.GetUsersFromUsernames(context.Background(), wantedMembersUsernames)
	if err != nil {
		return err
	}
	wantedMembersIDs := mapSlice(wantedMembers, func(user libwekan.User) libwekan.UserID { return user.ID })

	alreadyBoardMember, wantedInactiveBoardMember, ongoingBoardMember := intersect(currentMembersIDs, wantedMembersIDs)
	for _, userID := range alreadyBoardMember {
		err := wekan.EnsureUserIsActiveBoardMember(context.Background(), board.ID, userID)
		if err != nil {
			return err
		}
	}
	for _, userID := range ongoingBoardMember {
		err := wekan.EnsureUserIsActiveBoardMember(context.Background(), board.ID, userID)
		if err != nil {
			return err
		}
	}
	for _, userID := range wantedInactiveBoardMember {
		err := wekan.EnsureUserIsInactiveBoardMember(context.Background(), board.ID, userID)
		if err != nil {
			return err
		}
	}

	// globalWekan.AdminUser() est administrateur de toutes les boards, appliquons la règle
	return wekan.EnsureUserIsBoardAdmin(context.Background(), board.ID, libwekan.UserID(wekan.AdminID()))
}

func (users Users) inferBoardsMember() BoardsMembers {
	wekanBoardsUserSlice := make(map[libwekan.BoardSlug][]User)
	for _, user := range users {
		for _, boardSlug := range user.boards {
			if boardSlug != "" {
				boardSlug := libwekan.BoardSlug(boardSlug)
				wekanBoardsUserSlice[boardSlug] = append(wekanBoardsUserSlice[boardSlug], user)
			}
		}
	}

	wekanBoardsUsers := make(BoardsMembers)
	for boardSlug, userSlice := range wekanBoardsUserSlice {
		wekanBoardsUsers[boardSlug] = mapifySlice(userSlice, func(user User) Username { return user.email })
	}
	return wekanBoardsUsers
}

type BoardsMembers map[libwekan.BoardSlug]Users

func (boardsMembers BoardsMembers) AddBoards(boards []libwekan.Board) BoardsMembers {
	if boardsMembers == nil {
		boardsMembers = make(BoardsMembers)
	}
	for _, b := range boards {
		if _, ok := boardsMembers[b.Slug]; !ok {
			boardsMembers[b.Slug] = make(Users)
		}
	}
	return boardsMembers
}