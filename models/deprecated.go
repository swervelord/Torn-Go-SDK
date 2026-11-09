package models

import "encoding/json"

// Deprecated aliases for public names that the Torn API 6.15.7 contract renamed
// or removed. This file is hand-maintained (not generated); every alias points
// at a generated type with the same JSON shape, so the compiler keeps it honest.

// Attack log participants became component schemas in 6.15.7. The union types
// formerly named AttackLogAttacker and AttackLogDefender are now
// AttackLogAttackerField and AttackLogDefenderField, and AttackLogAttacker /
// AttackLogDefender name the participant objects themselves.
type (
	// Deprecated: use AttackLogAttacker (component schema since Torn API 6.15.7).
	AttackLogAttackerVariant1 = AttackLogAttacker
	// Deprecated: use AttackLogDefender (component schema since Torn API 6.15.7).
	AttackLogDefenderVariant1 = AttackLogDefender
	// Deprecated: use AttackLogAttackerItemField, the nullable AttackLogAttacker.Item union.
	AttackLogAttackerVariant1Item = AttackLogAttackerItemField
	// Deprecated: use AttackLogItem (component schema since Torn API 6.15.7). Its
	// ID and Name are plain required values instead of Optional wrappers.
	AttackLogAttackerVariant1ItemVariant1 = AttackLogItem
)

// Deprecated: use NewAttackLogAttackerItemFieldVariant1.
func NewAttackLogAttackerVariant1ItemVariant1(value AttackLogAttackerVariant1ItemVariant1) (AttackLogAttackerVariant1Item, error) {
	return NewAttackLogAttackerItemFieldVariant1(value)
}

// Deprecated: use NewAttackLogAttackerItemFieldVariant2.
func NewAttackLogAttackerVariant1ItemVariant2(value json.RawMessage) (AttackLogAttackerVariant1Item, error) {
	return NewAttackLogAttackerItemFieldVariant2(value)
}

// The unreferenced CompanySearchParameter, Parameters and UserSearchParameter
// schemas were removed in 6.15.7. They describe exactly the search filter items
// already generated for the search operations' filters parameters.
type (
	// Deprecated: use GetCompaniesSearchParamFiltersItem, the companies search filters item.
	CompanySearchParameter = GetCompaniesSearchParamFiltersItem
	// Deprecated: use GetCompaniesSearchParamFiltersItemVariant1.
	CompanySearchParameterVariant1 = GetCompaniesSearchParamFiltersItemVariant1
	// Deprecated: use GetFactionSearchParamFiltersItem, the faction search filters item.
	Parameters = GetFactionSearchParamFiltersItem
	// Deprecated: use GetFactionSearchParamFiltersItemVariant1.
	ParametersVariant1 = GetFactionSearchParamFiltersItemVariant1
	// Deprecated: use GetUsersSearchParamFiltersItem, the users search filters item.
	UserSearchParameter = GetUsersSearchParamFiltersItem
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1.
	UserSearchParameterVariant1 = GetUsersSearchParamFiltersItemVariant1
)

// Deprecated enum constants of the removed search parameter schemas.
const (
	// Deprecated: use GetCompaniesSearchParamFiltersItemVariant1NotRecruiting.
	CompanySearchParameterVariant1NotRecruiting = GetCompaniesSearchParamFiltersItemVariant1NotRecruiting
	// Deprecated: use GetCompaniesSearchParamFiltersItemVariant1Recruiting.
	CompanySearchParameterVariant1Recruiting = GetCompaniesSearchParamFiltersItemVariant1Recruiting
	// Deprecated: use GetFactionSearchParamFiltersItemVariant1Destroyed.
	ParametersVariant1Destroyed = GetFactionSearchParamFiltersItemVariant1Destroyed
	// Deprecated: use GetFactionSearchParamFiltersItemVariant1NotDestroyed.
	ParametersVariant1NotDestroyed = GetFactionSearchParamFiltersItemVariant1NotDestroyed
	// Deprecated: use GetFactionSearchParamFiltersItemVariant1NotRecruiting.
	ParametersVariant1NotRecruiting = GetFactionSearchParamFiltersItemVariant1NotRecruiting
	// Deprecated: use GetFactionSearchParamFiltersItemVariant1Recruiting.
	ParametersVariant1Recruiting = GetFactionSearchParamFiltersItemVariant1Recruiting
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1Enby.
	UserSearchParameterVariant1Enby = GetUsersSearchParamFiltersItemVariant1Enby
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1Female.
	UserSearchParameterVariant1Female = GetUsersSearchParamFiltersItemVariant1Female
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1InCompany.
	UserSearchParameterVariant1InCompany = GetUsersSearchParamFiltersItemVariant1InCompany
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1InFaction.
	UserSearchParameterVariant1InFaction = GetUsersSearchParamFiltersItemVariant1InFaction
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1InFederalJail.
	UserSearchParameterVariant1InFederalJail = GetUsersSearchParamFiltersItemVariant1InFederalJail
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1InHospital.
	UserSearchParameterVariant1InHospital = GetUsersSearchParamFiltersItemVariant1InHospital
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1InJail.
	UserSearchParameterVariant1InJail = GetUsersSearchParamFiltersItemVariant1InJail
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionDayAgo.
	UserSearchParameterVariant1LastActionDayAgo = GetUsersSearchParamFiltersItemVariant1LastActionDayAgo
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionHourAgo.
	UserSearchParameterVariant1LastActionHourAgo = GetUsersSearchParamFiltersItemVariant1LastActionHourAgo
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionMonthAgo.
	UserSearchParameterVariant1LastActionMonthAgo = GetUsersSearchParamFiltersItemVariant1LastActionMonthAgo
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionNow.
	UserSearchParameterVariant1LastActionNow = GetUsersSearchParamFiltersItemVariant1LastActionNow
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionRecent.
	UserSearchParameterVariant1LastActionRecent = GetUsersSearchParamFiltersItemVariant1LastActionRecent
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionWeekAgo.
	UserSearchParameterVariant1LastActionWeekAgo = GetUsersSearchParamFiltersItemVariant1LastActionWeekAgo
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1LastActionYearAgo.
	UserSearchParameterVariant1LastActionYearAgo = GetUsersSearchParamFiltersItemVariant1LastActionYearAgo
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1Male.
	UserSearchParameterVariant1Male = GetUsersSearchParamFiltersItemVariant1Male
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1Married.
	UserSearchParameterVariant1Married = GetUsersSearchParamFiltersItemVariant1Married
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotInCompany.
	UserSearchParameterVariant1NotInCompany = GetUsersSearchParamFiltersItemVariant1NotInCompany
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotInFaction.
	UserSearchParameterVariant1NotInFaction = GetUsersSearchParamFiltersItemVariant1NotInFaction
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotInFederalJail.
	UserSearchParameterVariant1NotInFederalJail = GetUsersSearchParamFiltersItemVariant1NotInFederalJail
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotInHospital.
	UserSearchParameterVariant1NotInHospital = GetUsersSearchParamFiltersItemVariant1NotInHospital
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotInJail.
	UserSearchParameterVariant1NotInJail = GetUsersSearchParamFiltersItemVariant1NotInJail
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotMarried.
	UserSearchParameterVariant1NotMarried = GetUsersSearchParamFiltersItemVariant1NotMarried
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1NotTraveling.
	UserSearchParameterVariant1NotTraveling = GetUsersSearchParamFiltersItemVariant1NotTraveling
	// Deprecated: use GetUsersSearchParamFiltersItemVariant1Traveling.
	UserSearchParameterVariant1Traveling = GetUsersSearchParamFiltersItemVariant1Traveling
)

// Deprecated: use GetCompaniesSearchParamFiltersItemVariant1Values.
func CompanySearchParameterVariant1Values() []CompanySearchParameterVariant1 {
	return GetCompaniesSearchParamFiltersItemVariant1Values()
}

// Deprecated: use NewGetCompaniesSearchParamFiltersItemVariant1.
func NewCompanySearchParameterVariant1(value CompanySearchParameterVariant1) (CompanySearchParameter, error) {
	return NewGetCompaniesSearchParamFiltersItemVariant1(value)
}

// Deprecated: use NewGetCompaniesSearchParamFiltersItemVariant2.
func NewCompanySearchParameterVariant2(value string) (CompanySearchParameter, error) {
	return NewGetCompaniesSearchParamFiltersItemVariant2(value)
}

// Deprecated: use GetFactionSearchParamFiltersItemVariant1Values.
func ParametersVariant1Values() []ParametersVariant1 {
	return GetFactionSearchParamFiltersItemVariant1Values()
}

// Deprecated: use NewGetFactionSearchParamFiltersItemVariant1.
func NewParametersVariant1(value ParametersVariant1) (Parameters, error) {
	return NewGetFactionSearchParamFiltersItemVariant1(value)
}

// Deprecated: use NewGetFactionSearchParamFiltersItemVariant2.
func NewParametersVariant2(value string) (Parameters, error) {
	return NewGetFactionSearchParamFiltersItemVariant2(value)
}

// Deprecated: use GetUsersSearchParamFiltersItemVariant1Values.
func UserSearchParameterVariant1Values() []UserSearchParameterVariant1 {
	return GetUsersSearchParamFiltersItemVariant1Values()
}

// Deprecated: use NewGetUsersSearchParamFiltersItemVariant1.
func NewUserSearchParameterVariant1(value UserSearchParameterVariant1) (UserSearchParameter, error) {
	return NewGetUsersSearchParamFiltersItemVariant1(value)
}

// Deprecated: use NewGetUsersSearchParamFiltersItemVariant2.
func NewUserSearchParameterVariant2(value string) (UserSearchParameter, error) {
	return NewGetUsersSearchParamFiltersItemVariant2(value)
}

// Deprecated: use NewGetUsersSearchParamFiltersItemVariant3.
func NewUserSearchParameterVariant3(value string) (UserSearchParameter, error) {
	return NewGetUsersSearchParamFiltersItemVariant3(value)
}
