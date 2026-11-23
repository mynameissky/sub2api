import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

import OrderStatusBadge from '../OrderStatusBadge.vue'

function mountBadge(status: string) {
  return mount(OrderStatusBadge, { props: { status } })
}

describe('OrderStatusBadge', () => {
  it('renders COMPLETED and the raw SUCCESS status as the same green success badge', () => {
    const completed = mountBadge('COMPLETED')
    const success = mountBadge('SUCCESS')

    expect(completed.text()).toBe('payment.status.completed')
    expect(success.text()).toBe('payment.status.completed')
    expect(success.classes()).toEqual(completed.classes())
    expect(completed.classes()).toContain('bg-green-100')
  })

  it('matches aliases regardless of case and surrounding whitespace', () => {
    expect(mountBadge(' success ').text()).toBe('payment.status.completed')
    expect(mountBadge('completed').text()).toBe('payment.status.completed')
  })

  it('keeps other statuses on their own labels, falling back to the raw value', () => {
    expect(mountBadge('PENDING').text()).toBe('payment.status.pending')
    expect(mountBadge('REFUNDED').text()).toBe('payment.status.refunded')

    const unknown = mountBadge('WEIRD_STATUS')
    expect(unknown.text()).toBe('WEIRD_STATUS')
    expect(unknown.classes()).toContain('bg-gray-100')
  })
})
