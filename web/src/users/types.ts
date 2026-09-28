export type OrgOption = { id: string; name: string }

// Issued is a temporary password just handed out, shown once so the
// administrator can pass it on.
export type Issued = { email: string; password: string; created: boolean }
