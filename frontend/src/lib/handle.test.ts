import { foldHandle } from './handle'

// Mirrors store.TestNormalizeHandle_FoldEquivalence on the server: same
// cases, same expected fold, so the two implementations cannot drift
// apart silently. foldHandle is query-key normalization only - the
// server remains the authority on identity.
it('folds case and underscores away, matching the server fold', () => {
  const cases = ['alice_prime', 'Alice_Prime', 'AlicePrime', 'ALICEPRIME', 'a_l_i_c_e_p_r_i_m_e']
  for (const c of cases) {
    expect(foldHandle(c)).toBe('aliceprime')
  }
})

it('touches only case and underscores, nothing else in the alphabet', () => {
  expect(foldHandle('Bob2')).toBe('bob2')
  expect(foldHandle('')).toBe('')
})
