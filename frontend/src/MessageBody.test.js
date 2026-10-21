import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/svelte'
import MessageBody from './MessageBody.svelte'

afterEach(cleanup)

describe('message formatting', () => {
  it('formats a recap label and separates list items and paragraphs', () => {
    const { container } = render(MessageBody, { props: { text: '**Missed follow-up:** Guest 125 asked for “another one.”\n\n- That request remains unanswered. (#15–16)\n- Try `history` to find the context.' } })
    expect(container.querySelector('strong').textContent).toBe('Missed follow-up:')
    expect(container.querySelectorAll('li')).toHaveLength(2)
    expect(container.querySelector('.message-inline-code').textContent).toBe('history')
    expect(container.textContent).not.toContain('**')
  })

  it('leaves HTML and code literal and excludes unsafe links and remote images', () => {
    const text = '<script>alert(1)</script>\n\n[unsafe](javascript:alert) [safe](https://example.com) ![diagram](https://example.com/private.png)\n\n```html\n  <img src=x onerror=alert(1)>\n```'
    const { container } = render(MessageBody, { props: { text } })
    expect(container.querySelector('script, img')).toBeNull()
    expect(container.textContent).toContain('<script>alert(1)</script>')
    expect(container.textContent).toContain('diagram')
    expect(container.querySelectorAll('a')).toHaveLength(1)
    expect(container.querySelector('a').getAttribute('href')).toBe('https://example.com/')
    expect(container.querySelector('pre code').textContent).toBe('  <img src=x onerror=alert(1)>\n')
    expect(container.querySelector('pre code').className).toBe('language-html')
  })

  it('counts words across nested formatting when wrapping a line', () => {
    const { container } = render(MessageBody, { props: { text: '**one two three four** five six *seven eight nine ten* eleven twelve' } })
    const paragraph = container.querySelector('p')
    function lines(node) {
      if (node.nodeName === 'BR') return '\n'
      if (node.nodeType === 3) return node.textContent
      return Array.from(node.childNodes).map(lines).join('')
    }
    expect(lines(paragraph).split('\n').map((line) => line.trim().split(/\s+/u).length)).toEqual([10, 2])
  })
})
