import { http, HttpResponse } from 'msw';

export const handlers = [
  http.get('/api/health', () => {
    return HttpResponse.json({ 
      status: 'ok', 
      message: 'This is a mocked response from MSW!' 
    });
  }),
];