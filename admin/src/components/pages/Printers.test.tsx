import { effectivePrinterStatus, printerStatusLabel } from './Printers';

describe('effectivePrinterStatus', () => {
  it('shows offline while the owning node is offline', () => {
    expect(effectivePrinterStatus('idle', 'offline')).toBe('offline');
    expect(effectivePrinterStatus('printer_out_of_paper', 'offline')).toBe('offline');
  });

  it('preserves printer state while the node is not offline', () => {
    expect(effectivePrinterStatus('idle', 'online')).toBe('idle');
    expect(effectivePrinterStatus('printer_out_of_paper', 'unstable')).toBe('printer_out_of_paper');
  });
});

describe('printerStatusLabel', () => {
  it('maps printer faults instead of displaying protocol codes', () => {
    expect(printerStatusLabel('printer_out_of_paper')).toBe('缺纸');
  });

  it('uses a generic label for an unknown protocol state', () => {
    expect(printerStatusLabel('some_future_status')).toBe('未知状态');
  });
});
