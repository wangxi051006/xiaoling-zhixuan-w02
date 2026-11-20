package identity

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAddressInput    = errors.New("invalid address or unavailable building")
	ErrObjectNotFound  = errors.New("object not found")
	ErrVersionConflict = errors.New("object version changed")
)

type Directory struct{ pool *pgxpool.Pool }

func NewDirectory(pool *pgxpool.Pool) *Directory { return &Directory{pool: pool} }

var _ contract.IdentityPort = (*Directory)(nil)

type AddressInput struct {
	BuildingID   string `json:"building_id"`
	Room         string `json:"room"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
}

type AddressPatch struct {
	BuildingID   *string `json:"building_id"`
	Room         *string `json:"room"`
	ContactName  *string `json:"contact_name"`
	ContactPhone *string `json:"contact_phone"`
}

func trimText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\x00\r\n\t")
}

func normalizePhone(phone string) string {
	if len(phone) == 11 && phone[0] == '1' {
		return "+86" + phone
	}
	return phone
}

func (a AddressInput) validated() (AddressInput, error) {
	var roomOK, nameOK, buildingOK bool
	a.BuildingID, buildingOK = trimText(a.BuildingID, 128)
	a.Room, roomOK = trimText(a.Room, 40)
	a.ContactName, nameOK = trimText(a.ContactName, 60)
	a.ContactPhone = normalizePhone(a.ContactPhone)
	if !buildingOK || !roomOK || !nameOK || !phonePattern.MatchString(a.ContactPhone) {
		return AddressInput{}, ErrAddressInput
	}
	return a, nil
}

type addressScanner interface{ Scan(...any) error }

func scanAddress(row addressScanner) (contract.AddressSnapshot, error) {
	var a contract.AddressSnapshot
	err := row.Scan(&a.AddressID, &a.StationID, &a.BuildingID, &a.BuildingName, &a.Room, &a.ContactName, &a.ContactPhone, &a.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrObjectNotFound
	}
	return a, err
}

const addressColumns = `d.id,a.station_id,d.building_id,b.name,d.room,d.contact_name,d.contact_phone,d.version`
const addressFrom = ` FROM addresses d JOIN accounts a ON a.id=d.account_id
	JOIN service_buildings b ON b.id=d.building_id AND b.station_id=a.station_id
	WHERE d.account_id=$1 AND d.deleted_at IS NULL AND a.enabled AND a.account_type='CUSTOMER'`

func (s *Directory) ResolveAddress(ctx context.Context, tx pgx.Tx, accountID, addressID string) (contract.AddressSnapshot, error) {
	return scanAddress(tx.QueryRow(ctx, `SELECT `+addressColumns+addressFrom+` AND d.id=$2 AND b.enabled FOR SHARE OF d,b,a`, accountID, addressID))
}

type ServiceBuilding struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Directory) ListServiceBuildings(ctx context.Context, stationID, cursor string, limit int) ([]ServiceBuilding, error) {
	if limit < 1 || limit > 101 {
		return nil, ErrAddressInput
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name FROM service_buildings
		WHERE station_id=$1 AND enabled AND id>$2 ORDER BY id LIMIT $3`, stationID, cursor, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ServiceBuilding])
}

func (s *Directory) ListAddresses(ctx context.Context, accountID, stationID, cursor string, limit int) ([]contract.AddressSnapshot, error) {
	if limit < 1 || limit > 101 {
		return nil, ErrAddressInput
	}
	rows, err := s.pool.Query(ctx, `SELECT `+addressColumns+addressFrom+` AND a.station_id=$2 AND d.id>$3 ORDER BY d.id LIMIT $4`, accountID, stationID, cursor, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (contract.AddressSnapshot, error) { return scanAddress(row) })
}

func (s *Directory) CreateAddress(ctx context.Context, accountID, stationID string, input AddressInput) (contract.AddressSnapshot, error) {
	a, err := input.validated()
	if err != nil {
		return contract.AddressSnapshot{}, err
	}
	var result contract.AddressSnapshot
	err = platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		id := secretToken()
		tag, err := tx.Exec(ctx, `INSERT INTO addresses (id,account_id,building_id,room,contact_name,contact_phone)
			SELECT $1,a.id,b.id,$5,$6,$7 FROM accounts a JOIN service_buildings b ON b.station_id=a.station_id
			WHERE a.id=$2 AND a.station_id=$3 AND a.enabled AND a.account_type='CUSTOMER' AND b.id=$4 AND b.enabled`, id, accountID, stationID, a.BuildingID, a.Room, a.ContactName, a.ContactPhone)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrAddressInput
		}
		result, err = s.ResolveAddress(ctx, tx, accountID, id)
		return err
	})
	return result, err
}

func (s *Directory) UpdateAddress(ctx context.Context, accountID, stationID, addressID string, expected int64, patch AddressPatch) (contract.AddressSnapshot, error) {
	var result contract.AddressSnapshot
	if expected < 1 || (patch.BuildingID == nil && patch.Room == nil && patch.ContactName == nil && patch.ContactPhone == nil) {
		return result, ErrAddressInput
	}
	err := platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := scanAddress(tx.QueryRow(ctx, `SELECT `+addressColumns+addressFrom+` AND a.station_id=$2 AND d.id=$3 FOR UPDATE OF d`, accountID, stationID, addressID))
		if err != nil {
			return err
		}
		if before.Version != expected {
			return ErrVersionConflict
		}
		input := AddressInput{before.BuildingID, before.Room, before.ContactName, before.ContactPhone}
		for dst, src := range map[*string]*string{&input.BuildingID: patch.BuildingID, &input.Room: patch.Room, &input.ContactName: patch.ContactName, &input.ContactPhone: patch.ContactPhone} {
			if src != nil {
				*dst = *src
			}
		}
		input, err = input.validated()
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE addresses SET building_id=$2,room=$3,contact_name=$4,contact_phone=$5,version=version+1
			WHERE id=$1 AND EXISTS(SELECT 1 FROM service_buildings WHERE id=$2 AND station_id=$6 AND enabled)`, addressID, input.BuildingID, input.Room, input.ContactName, input.ContactPhone, stationID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrAddressInput
		}
		result, err = s.ResolveAddress(ctx, tx, accountID, addressID)
		return err
	})
	return result, err
}

func (s *Directory) DeleteAddress(ctx context.Context, accountID, stationID, addressID string, expected int64) error {
	if expected < 1 {
		return ErrAddressInput
	}
	return platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := scanAddress(tx.QueryRow(ctx, `SELECT `+addressColumns+addressFrom+` AND a.station_id=$2 AND d.id=$3 FOR UPDATE OF d`, accountID, stationID, addressID))
		if err != nil {
			return err
		}
		if a.Version != expected {
			return ErrVersionConflict
		}
		_, err = tx.Exec(ctx, `UPDATE addresses SET deleted_at=now(),version=version+1 WHERE id=$1`, addressID)
		return err
	})
}
