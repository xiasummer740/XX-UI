package database

import (
	"github.com/XiaSummer740/XX-UI/database/model"
)

// GetRemoteServers retrieves all remote servers from the database.
func GetRemoteServers() ([]*model.RemoteServer, error) {
	db := GetDB()
	var servers []*model.RemoteServer
	err := db.Find(&servers).Error
	if err != nil {
		return nil, err
	}
	return servers, nil
}

// GetRemoteServerByID retrieves a single remote server by its ID.
func GetRemoteServerByID(id int) (*model.RemoteServer, error) {
	db := GetDB()
	var server model.RemoteServer
	err := db.Where("id = ?", id).First(&server).Error
	if err != nil {
		return nil, err
	}
	return &server, nil
}

// CreateRemoteServer inserts a new remote server record.
func CreateRemoteServer(server *model.RemoteServer) error {
	db := GetDB()
	return db.Create(server).Error
}

// UpdateRemoteServer updates an existing remote server record.
func UpdateRemoteServer(server *model.RemoteServer) error {
	db := GetDB()
	return db.Save(server).Error
}

// DeleteRemoteServer removes a remote server record by its ID.
func DeleteRemoteServer(id int) error {
	db := GetDB()
	return db.Delete(&model.RemoteServer{}, id).Error
}
