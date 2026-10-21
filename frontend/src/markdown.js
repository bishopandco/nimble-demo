import { Lexer } from 'marked'
import { messageBlocks } from './thread.js'

// Build an allowlisted view tree. Message content never becomes raw HTML.
function measuredText(text, line) {
  const nodes = []
  for (const part of text.split(/(\s+)/u)) {
    if (!part) continue
    if (/^\s+$/u.test(part)) {
      line.inWord = false
      if (/[\r\n]/u.test(part)) line.words = 0
    } else if (!line.inWord) {
      if (line.words === 10) {
        nodes.push({ type: 'br' })
        line.words = 0
      }
      line.words++
      line.inWord = true
    }
    nodes.push({ type: 'text', text: part })
  }
  return nodes
}

function safeLink(href) {
  try {
    const url = new URL(href)
    return ['http:', 'https:', 'mailto:'].includes(url.protocol) ? url.href : null
  } catch { return null }
}

function inlineNodes(tokens, line = { words: 0, inWord: false }, depth = 0) {
  return tokens.flatMap((token) => {
    if (depth > 32) return measuredText(token.raw || token.text || '', line)
    if (token.type === 'br') {
      line.words = 0
      line.inWord = false
      return [{ type: 'br' }]
    }
    if (['strong', 'em', 'del', 'link'].includes(token.type)) {
      const children = inlineNodes(token.tokens || [], line, depth + 1)
      return [{ type: token.type, children, href: token.type === 'link' ? safeLink(token.href) : null, title: token.title || undefined }]
    }
    if (token.type === 'codespan') return [{ type: 'inline-code', children: measuredText(token.text, line) }]
    if (token.type === 'text' && token.tokens) return inlineNodes(token.tokens, line, depth + 1)
    if (token.type === 'checkbox') return measuredText(token.checked ? '☑ ' : '☐ ', line)
    // Images render their alt text; HTML tokens remain visible, escaped text.
    return measuredText(token.text ?? token.raw ?? '', line)
  })
}

function blockNodes(tokens, depth = 0) {
  return tokens.flatMap((token) => {
    if (['space', 'def'].includes(token.type)) return []
    if (depth > 16) return [{ type: 'paragraph', children: measuredText(token.raw || '', { words: 0, inWord: false }) }]
    if (token.type === 'code') {
      const language = (token.lang || '').trim().split(/\s+/u)[0]
      return [{ type: 'code', text: token.text, language: /^[\w.+#-]{1,32}$/u.test(language) ? language : '' }]
    }
    if (token.type === 'hr') return [{ type: 'hr' }]
    if (token.type === 'blockquote') return [{ type: 'blockquote', children: blockNodes(token.tokens, depth + 1) }]
    if (token.type === 'list') {
      return [{ type: 'list', ordered: token.ordered, start: token.start || 1, items: token.items.map((item) => blockNodes(item.tokens, depth + 1)) }]
    }
    if (token.type === 'table') {
      const cells = (row) => row.map((cell) => ({ children: inlineNodes(cell.tokens), align: cell.align || 'left' }))
      return [{ type: 'table', header: cells(token.header), rows: token.rows.map(cells) }]
    }
    const children = inlineNodes(token.tokens || [{ type: 'text', text: token.text ?? token.raw ?? '' }])
    return [{ type: token.type === 'heading' ? 'heading' : 'paragraph', children, level: Math.min(6, (token.depth || 1) + 2) }]
  })
}

export function messageMarkdown(text) {
  // Retain the existing fence parser's exact code whitespace and partial fences.
  return messageBlocks(text).flatMap((block) => {
    if (block.type === 'code') return [block]
    try {
      return blockNodes(Lexer.lex(block.text, { gfm: true }))
    } catch {
      return [{ type: 'paragraph', children: measuredText(block.text, { words: 0, inWord: false }) }]
    }
  })
}
