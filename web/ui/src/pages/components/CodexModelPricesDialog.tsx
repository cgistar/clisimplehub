import { useEffect, useState } from 'react'
import type { CodexModelPrice } from '@/types'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface CodexModelPricesDialogProps {
  open: boolean
  prices: CodexModelPrice[]
  loading: boolean
  saving: boolean
  onOpenChange: (open: boolean) => void
  onSave: (prices: CodexModelPrice[]) => Promise<void>
}

function copyPrices(prices: CodexModelPrice[]): CodexModelPrice[] {
  return prices.map((price) => ({
    model: price.model,
    inputPer1M: price.inputPer1M,
    cachedInputPer1M: price.cachedInputPer1M,
    cacheWritePer1M: price.cacheWritePer1M,
    outputPer1M: price.outputPer1M,
  }))
}

export default function CodexModelPricesDialog({ open, prices, loading, saving, onOpenChange, onSave }: CodexModelPricesDialogProps) {
  const [rows, setRows] = useState<CodexModelPrice[]>([])
  const [validationError, setValidationError] = useState<string>('')

  useEffect(() => {
    if (open) {
      setRows(copyPrices(prices))
      setValidationError('')
    }
  }, [open, prices])

  function updateRow(index: number, field: keyof CodexModelPrice, value: string): void {
    setRows((current) => current.map((row, rowIndex) => {
      if (rowIndex !== index) return row
      if (field === 'model') return { ...row, model: value }
      return { ...row, [field]: Number(value) }
    }))
  }

  function addRow(): void {
    setRows((current) => [...current, { model: '', inputPer1M: 0, cachedInputPer1M: 0, cacheWritePer1M: 0, outputPer1M: 0 }])
  }

  function removeRow(index: number): void {
    setRows((current) => current.filter((_, rowIndex) => rowIndex !== index))
  }

  async function submit(): Promise<void> {
    const seen = new Set<string>()
    const normalized = copyPrices(rows)
    for (const price of normalized) {
      price.model = price.model.trim()
      if (!price.model) {
        setValidationError('模型名不能为空')
        return
      }
      if (seen.has(price.model)) {
        setValidationError(`模型名重复：${price.model}`)
        return
      }
      seen.add(price.model)
      if ([price.inputPer1M, price.cachedInputPer1M, price.cacheWritePer1M, price.outputPer1M].some((amount) => !Number.isFinite(amount) || amount < 0)) {
        setValidationError(`模型 ${price.model} 的价格必须是非负有限数`)
        return
      }
    }
    setValidationError('')
    try {
      await onSave(normalized)
    } catch (error) {
      setValidationError(error instanceof Error ? error.message : '保存模型单价失败')
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="model-prices-dialog" closeDisabled={saving}>
        <DialogHeader>
          <div>
            <DialogTitle>模型单价</DialogTitle>
            <DialogDescription>单位为 USD / 1M Token。预估成本仅基于本地 Token 统计，不代表 OpenAI 实际账单、订阅余额或额度。</DialogDescription>
          </div>
        </DialogHeader>
        <DialogBody>
          {loading ? <div className="empty-state">正在加载模型单价...</div> : (
            <div className="model-prices-table-wrap">
              <table className="model-prices-table">
                <thead><tr><th>模型</th><th>输入 / 1M</th><th>缓存读取 / 1M</th><th>缓存写入 / 1M</th><th>输出 / 1M</th><th></th></tr></thead>
                <tbody>
                  {rows.map((price, index) => (
                    <tr key={`${index}-${price.model}`}>
                      <td><input className="input" value={price.model} onChange={(event) => updateRow(index, 'model', event.target.value)} placeholder="例如 gpt-5.6-sol" /></td>
                      <td><input className="input" type="number" min="0" step="any" value={price.inputPer1M} onChange={(event) => updateRow(index, 'inputPer1M', event.target.value)} /></td>
                      <td><input className="input" type="number" min="0" step="any" value={price.cachedInputPer1M} onChange={(event) => updateRow(index, 'cachedInputPer1M', event.target.value)} /></td>
                      <td><input className="input" type="number" min="0" step="any" value={price.cacheWritePer1M} onChange={(event) => updateRow(index, 'cacheWritePer1M', event.target.value)} /></td>
                      <td><input className="input" type="number" min="0" step="any" value={price.outputPer1M} onChange={(event) => updateRow(index, 'outputPer1M', event.target.value)} /></td>
                      <td><button className="btn danger" type="button" disabled={saving} onClick={() => removeRow(index)}>删除</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {validationError ? <div className="notice danger-notice mt-12">{validationError}</div> : null}
        </DialogBody>
        <DialogFooter>
          <button className="btn" type="button" disabled={loading || saving} onClick={addRow}>添加模型</button>
          <span className="spacer" />
          <button className="btn" type="button" disabled={saving} onClick={() => onOpenChange(false)}>取消</button>
          <button className="btn primary" type="button" disabled={loading || saving} onClick={() => void submit()}>{saving ? '保存中...' : '保存'}</button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
