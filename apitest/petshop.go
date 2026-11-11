package apitest

// PetShop is the definition of an invented API, a pet shop on example.test,
// for tests that need a definition and have none of their own. It is loaded
// with definition.Load, and a test that needs more gives an overlay.
//
// The shop has two environments, production and staging, with two APIs
// each: listings go to "admin" and single items to "main", unless a
// resource says otherwise. An ID is eight hexadecimal digits.
//
//   - owners are also known by their membership number, which a filter
//     finds (an ID alias).
//   - pets are also known by their tag, such as REX-7, which a full-text
//     search finds among other things (an ID alias that names its field).
//   - visits are a sub-resource of pets and live on the admin API.
//   - breeds live on the main API and are also known by their name, which
//     only the whole listing tells (an ID search).
//   - orders are listed for one owner only (a required filter), who may be
//     given by membership number (a filter rule).
//   - receipts cannot be listed, only fetched by ID.
const PetShop = `
environments:
  production:
    apis:
      main: https://api.example.test/v1
      admin: https://admin.example.test/v1
    linkBase: https://shop.example.test
  staging:
    apis:
      main: https://api.staging.example.test/v1
      admin: https://admin.staging.example.test/v1
    linkBase: https://shop.staging.example.test
defaults:
  api: admin
  itemApi: main
identifiers:
  native: "^[0-9a-f]{8}$"
  display: [tag, number]
filterIds:
  ownerId:
    - {match: "^[0-9]{1,7}$", resource: owners}
  about:
    - {match: "^[A-Z]+-[0-9]+$", resource: pets}
    - {match: "^[0-9]{1,7}$", resource: owners}
resources:
  owners:
    columns: [number, name, city]
    filters: ["city=", "number="]
    idAliases:
      - {match: "^[0-9]{1,7}$", query: "number=${id}", field: number}
  pets:
    columns: [tag, name, species, ownerId]
    filters: ["species=", "ownerId=", "search="]
    idAliases:
      - {match: "^[A-Z]+-[0-9]+$", query: 'search="${id}"', field: tag}
  visits:
    parent: pets
    api: admin
    columns: [date, reason]
    filters: ["reason="]
  breeds:
    api: main
    columns: [name, species]
    idSearch:
      - {match: "^[0-9a-f]{8}$", not: true, column: name}
  orders:
    columns: [id, ownerId, status, total]
    filters: ["ownerId=", "status=", "about="]
    requiredFilters: [ownerId]
  receipts:
    supportsListing: false
    columns: [id, orderId, total]
`
