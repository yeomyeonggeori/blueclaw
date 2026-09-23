import { redirect } from 'react-router';
import { docsRoute } from '@/lib/shared';

export function clientLoader() {
  return redirect(docsRoute);
}

export default function Home() {
  return null;
}
