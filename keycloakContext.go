package main

import (
	"context"
	"log"
	"strings"

	"github.com/Nerzal/gocloak/v11"
	"github.com/pkg/errors"
	// 	"github.com/spf13/viper"
)

// KeycloakContext carry keycloak state
type KeycloakContext struct {
	hostname    string
	realm       string
	username    string
	password    string
	API         gocloak.GoCloak
	JWT         *gocloak.JWT
	Realm       *gocloak.RealmRepresentation
	Clients     []*gocloak.Client
	Users       []*gocloak.User
	Roles       []*gocloak.Role
	ClientRoles map[string][]*gocloak.Role
}

// NewKeycloakContext provides a connected keycloak context object
func NewKeycloakContext(hostname, realm, username, password string) (KeycloakContext, error) {
	kc := KeycloakContext{
		hostname: hostname,
		realm:    realm,
		username: username,
		password: password,
	}

	kc.API = gocloak.NewClient(kc.hostname)
	var err error
	kc.JWT, err = kc.API.LoginAdmin(context.Background(), kc.username, kc.password, kc.realm)
	if err != nil {
		return KeycloakContext{}, err
	}

	// fetch Realm
	kc.Realm, err = kc.API.GetRealm(context.Background(), kc.JWT.AccessToken, kc.realm)
	if err != nil {
		return KeycloakContext{}, err
	}

	err = kc.refreshClients()
	if err != nil {
		return KeycloakContext{}, err
	}

	err = kc.refreshUsers()
	if err != nil {
		return KeycloakContext{}, err
	}

	kc.Roles, err = kc.API.GetRealmRoles(context.Background(), kc.JWT.AccessToken, kc.realm, gocloak.GetRoleParams{})
	if err != nil {
		return KeycloakContext{}, err
	}

	err = kc.refreshClientRoles()
	if err != nil {
		return KeycloakContext{}, err
	}

	return kc, nil
}

// GetRoles returns realm roles in []string
func (kc KeycloakContext) GetRoles() Roles {
	var roles Roles
	for _, r := range kc.Roles {
		if r != nil && r.Name != nil {
			roles.add(*r.Name)
		}
	}
	return roles
}

// CreateClientRoles creates a bunch of roles in a client from a []string
func (kc *KeycloakContext) CreateClientRoles(clientID string, roles Roles) (int, error) {
	defer func() {
		if err := kc.refreshClientRoles(); err != nil {
			log.Fatalf("error refreshing client roles : %s", err)
		}
	}()

	internalClientID, err := kc.GetInternalIDFromClientID(clientID)
	if err != nil {
		return 0, errors.Errorf("kc.CreateClientRoles, %s: no such client", clientID)
	}

	i := 0
	for _, role := range roles {
		if kc.GetClientRoles()[clientID].contains(role) {
			return i, errors.Errorf("kc.CreateClientRoles, %s: role already exists", role)
		}
		kcRole := gocloak.Role{
			Name: &role,
		}
		_, err := kc.API.CreateClientRole(context.Background(), kc.JWT.AccessToken, kc.realm, internalClientID, kcRole)
		if err != nil {
			return i, errors.Errorf("kc.CreateClientRoles, %s: could not create roles, %s", role, err.Error())
		}
		i++
	}
	return i, nil
}

// GetInternalIDFromClientID resolves internal client ID from human readable clientID
func (kc KeycloakContext) GetInternalIDFromClientID(clientID string) (string, error) {
	for _, c := range kc.Clients {
		if c != nil && c.ClientID != nil {
			if *c.ClientID == clientID {
				return *c.ID, nil
			}
		}
	}
	return "", errors.Errorf("kc.GetInternalIDFromClientID %s: no such clientID", clientID)
}

// GetQuietlyInternalIDFromClientID resolves internal client ID from human readable clientID
func (kc KeycloakContext) GetQuietlyInternalIDFromClientID(clientID string) (string, bool) {
	id, err := kc.GetInternalIDFromClientID(clientID)
	if err != nil {
		return "", false
	}
	return id, true
}

// GetClientRoles returns realm roles in map[string][]string
func (kc KeycloakContext) GetClientRoles() map[string]Roles {
	clientRoles := make(map[string]Roles)
	for n, c := range kc.ClientRoles {
		var roles []string
		for _, r := range c {
			roles = append(roles, *r.Name)
		}
		clientRoles[n] = roles
	}
	return clientRoles
}

func (kc *KeycloakContext) refreshClients() error {
	var err error
	kc.Clients, err = kc.API.GetClients(context.Background(), kc.JWT.AccessToken, kc.realm, gocloak.GetClientsParams{})
	return err
}

// refreshUsers pulls user base from keycloak server
func (kc *KeycloakContext) refreshUsers() error {
	var err error
	max := 100000
	kc.Users, err = kc.API.GetUsers(context.Background(), kc.JWT.AccessToken, kc.realm, gocloak.GetUsersParams{
		Max: &max,
	})
	return err
}

func (kc *KeycloakContext) refreshClientRoles() error {
	kc.ClientRoles = make(map[string][]*gocloak.Role)
	for _, c := range kc.Clients {
		if c != nil && c.ClientID != nil {
			roles, err := kc.API.GetClientRoles(context.Background(), kc.JWT.AccessToken, kc.realm, *c.ID, gocloak.GetRoleParams{})
			if err != nil {
				return err
			}
			kc.ClientRoles[*c.ClientID] = roles
		}
	}
	return nil
}

// CreateUsers sends a slice of gocloak Users to keycloak
func (kc *KeycloakContext) CreateUsers(users []gocloak.User, userMap Users, clientName string) error {
	internalID, err := kc.GetInternalIDFromClientID(clientName)
	if err != nil {
		return err
	}
	for _, user := range users {
		log.Printf("kc.CreateUsers - %s: creating user", *user.Username)
		u, err := kc.API.CreateUser(context.Background(), kc.JWT.AccessToken, kc.realm, user)
		if err != nil {
			log.Printf("kc.CreateUsers - %s: unable to create user, %s", *user.Username, err.Error())
		}

		roles := userMap[*user.Username].roles().GetKeycloakRoles(clientName, *kc)
		log.Printf("kc.CreateUsers - %s: adding roles to user [%s]", *user.Username, strings.Join(rolesFromGocloakRoles(roles), ", "))
		err = kc.API.AddClientRoleToUser(context.Background(), kc.JWT.AccessToken, kc.realm, internalID, u, roles)
		if err != nil {
			var role []string
			for _, r := range roles {
				role = append(role, *r.Name)
			}
			log.Printf("error adding client rôles (%s) to %s: %s", strings.Join(role, ","), *user.Email, err.Error())
		}
	}

	err = kc.refreshUsers()
	return err
}

// DisableUsers disables users and deletes every roles of users
func (kc *KeycloakContext) DisableUsers(users []gocloak.User, clientName string) error {
	f := false
	internalID, err := kc.GetInternalIDFromClientID(clientName)
	if err != nil {
		return err
	}
	for _, u := range users {
		//if *u.username == viper.GetString("username") {
		//	continue
		//}
		u.Enabled = &f
		log.Printf("kc.DisableUsers - %s: disabling user", *u.Username)
		err := kc.API.UpdateUser(context.Background(), kc.JWT.AccessToken, kc.realm, u)
		if err != nil {
			log.Printf("kc.DisableUsers - %s: error disabling user, %s", *u.Username, err.Error())
			return err
		}
		roles, err := kc.API.GetClientRolesByUserID(context.Background(), kc.JWT.AccessToken, kc.realm, internalID, *u.ID)
		if err != nil {
			log.Printf("kc.DisableUsers - %s: failed to retrieve roles for user, %s", *u.Username, err.Error())
		}
		var ro []gocloak.Role
		for _, r := range roles {
			ro = append(ro, *r)
		}

		log.Printf("kc.DisableUsers - %s: remove roles from user [%s]", *u.Username, strings.Join(rolesFromGocloakPRoles(roles), ", "))
		err = kc.API.DeleteClientRoleFromUser(context.Background(), kc.JWT.AccessToken, kc.realm, internalID, *u.ID, ro)
		if err != nil {
			log.Printf("kc.DisableUsers - %s: failed to remove roles, %s", *u.Username, err.Error())
			return err
		}
	}
	err = kc.refreshUsers()
	return err
}

// EnableUsers enables users and adds roles
func (kc *KeycloakContext) EnableUsers(users []gocloak.User) error {
	t := true
	for _, user := range users {
		log.Printf("kc.EnableUsers - %s: enabling user", *user.Username)
		user.Enabled = &t
		err := kc.API.UpdateUser(context.Background(), kc.JWT.AccessToken, kc.realm, user)
		if err != nil {
			log.Printf("kc.EnableUsers - %s: failed to enable user: %s", *user.Username, err.Error())
		}
	}
	err := kc.refreshUsers()
	return err
}

// UpdateCurrentUsers sets client roles on specified users according userMap
func (kc KeycloakContext) UpdateCurrentUsers(users []gocloak.User, userMap Users, clientName string) error {
	accountInternalID, err := kc.GetInternalIDFromClientID("account")
	if err != nil {
		return err
	}
	internalID, err := kc.GetInternalIDFromClientID(clientName)
	if err != nil {
		return err
	}

	for _, user := range users {
		roles, err := kc.API.GetClientRolesByUserID(context.Background(), kc.JWT.AccessToken, kc.realm, internalID, *user.ID)
		if err != nil {
			return err
		}
		accountPRoles, err := kc.API.GetClientRolesByUserID(context.Background(), kc.JWT.AccessToken, kc.realm, accountInternalID, *user.ID)
		if err != nil {
			return err
		}
		accountRoles := rolesFromGocloakPRoles(accountPRoles)

		u := userMap[*user.Username]
		ug := u.ToGocloakUser()
		if user.LastName != nil && u.nom != *user.LastName ||
			user.LastName != nil && u.prenom != *user.FirstName ||
			!compareAttributes(user.Attributes, ug.Attributes) {

			update := gocloak.User{
				ID:         user.ID,
				FirstName:  &u.prenom,
				LastName:   &u.nom,
				Attributes: ug.Attributes,
			}

			log.Printf("kc.UpdateCurrentUsers - %s: updating user names and attributes", *user.Username)
			err := kc.API.UpdateUser(context.Background(), kc.JWT.AccessToken, kc.realm, update)
			if err != nil {
				log.Printf("kc.UpdateCurrentUsers - %s: failed to update user names, %s", *user.Username, err.Error())
				return err
			}
		}

		novel, old := userMap[*user.Username].roles().compare(rolesFromGocloakPRoles(roles))
		if len(old) > 0 {
			log.Printf("kc.UpdateCurrentUsers - %s: deleting unused roles [%s]", *user.Username, strings.Join(old, ", "))
			err = kc.API.DeleteClientRoleFromUser(context.Background(), kc.JWT.AccessToken, kc.realm, internalID, *user.ID, old.GetKeycloakRoles(clientName, kc))
			if err != nil {
				log.Printf("kc.UpdateCurrentUsers - %s: failed to delete roles, %s", *user.Username, err.Error())
			}
		}

		if len(novel) > 0 {
			log.Printf("kc.UpdateCurrentUsers - %s: adding missing roles [%s]", *user.Username, strings.Join(novel, ", "))
			err = kc.API.AddClientRoleToUser(context.Background(), kc.JWT.AccessToken, kc.realm, internalID, *user.ID, novel.GetKeycloakRoles(clientName, kc))
			if err != nil {
				log.Printf("kc.UpdateCurrentUsers - %s: failed to add roles, %s", *user.Username, err.Error())
			}
		}

		if len(accountRoles) > 0 {
			log.Printf("kc.ProtectCurrentUsers - %s: disabling account management [%s]", *user.Username, strings.Join(accountRoles, ", "))
			err = kc.API.DeleteClientRoleFromUser(context.Background(), kc.JWT.AccessToken, kc.realm, accountInternalID, *user.ID, accountRoles.GetKeycloakRoles("account", kc))
			if err != nil {
				log.Printf("kc.ProtectUsers - %s: failed to disable management, %s", *user.Username, err.Error())
			}
		}
	}

	return nil
}

// SaveMasterRealm update master Realm
func (kc KeycloakContext) SaveMasterRealm(input gocloak.RealmRepresentation) {
	id := "master"
	input.ID = &id
	input.Realm = &id
	if err := kc.API.UpdateRealm(context.Background(), kc.JWT.AccessToken, input); err != nil {
		log.Panicf("Error when updating Realm : %s", err.Error())
	}
}

// SaveClients save clients then refresh clients list
func (kc *KeycloakContext) SaveClients(input []*gocloak.Client) {
	for _, client := range input {
		kc.saveClient(*client)
	}
	err := kc.refreshClients()
	if err != nil {
		log.Panicf("Error saving clients : %s", err)
	}
}

func (kc KeycloakContext) saveClient(input gocloak.Client) {
	id, found := kc.GetQuietlyInternalIDFromClientID(*input.ClientID)
	// need client creation
	if !found {
		createClient, err := kc.API.CreateClient(context.Background(), kc.JWT.AccessToken, kc.realm, input)
		if err != nil {
			log.Panicf("error creating client : %s", err)
		}
		log.Printf("new client has id : %s", createClient)
		return
	}
	// update client
	input.ID = &id
	if err := kc.API.UpdateClient(context.Background(), kc.JWT.AccessToken, kc.realm, input); err != nil {
		log.Panicf("error updating client : %s", err)
	}
}
