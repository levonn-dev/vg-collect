import { moveByOffset, neighborIDs } from './reorder'

const ids = ['a', 'b', 'c', 'd']

it('moving down places the item between its new neighbors', () => {
  // a dropped onto c: b c a d -> after c, before d
  expect(neighborIDs(ids, 'a', 'c')).toEqual({ after_id: 'c', before_id: 'd' })
})

it('moving up places the item between its new neighbors', () => {
  // d dropped onto b: a d b c -> after a, before b
  expect(neighborIDs(ids, 'd', 'b')).toEqual({ after_id: 'a', before_id: 'b' })
})

it('moving to the top yields a null after_id', () => {
  expect(neighborIDs(ids, 'c', 'a')).toEqual({ after_id: null, before_id: 'a' })
})

it('moving to the bottom yields a null before_id', () => {
  expect(neighborIDs(ids, 'a', 'd')).toEqual({ after_id: 'd', before_id: null })
})

it('a no-op or unknown id maps to null', () => {
  expect(neighborIDs(ids, 'b', 'b')).toBeNull()
  expect(neighborIDs(ids, 'zz', 'b')).toBeNull()
  expect(neighborIDs(ids, 'b', 'zz')).toBeNull()
})

it('moveByOffset steps one slot and stops at the edges', () => {
  expect(moveByOffset(ids, 'b', 1)).toEqual({ after_id: 'c', before_id: 'd' })
  expect(moveByOffset(ids, 'b', -1)).toEqual({ after_id: null, before_id: 'a' })
  expect(moveByOffset(ids, 'a', -1)).toBeNull()
  expect(moveByOffset(ids, 'd', 1)).toBeNull()
})
